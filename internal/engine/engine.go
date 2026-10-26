// Package engine runs the scan loop: search, filter, evaluate, alert.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/prefilter"
	"github.com/cherrymcgerry/scharrelaar/internal/score"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/telegram"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
	"github.com/cherrymcgerry/scharrelaar/internal/websearch"
)

const (
	keywordLimit = 30
	// browseLimit is the page size for searches without a keyword: the "Niet werkend"
	// search and browsing a subcategory.
	browseLimit = 100
	notWorking  = "Niet werkend"
)

// Engine scans the configured watches.
type Engine struct {
	Config  config.Config
	LLM     *llm.Manager
	Store   *store.Store
	Bot     *telegram.Bot
	ChatID  int64
	Options llm.Options
	// Delay between requests to a marketplace.
	Delay time.Duration

	// searches caches search results within one scan, so watches that share a
	// query (the repair templates all search "Niet werkend") ask the site once.
	searches map[string][]market.Listing
	// vision is the photo model for this scan, or "" when it is not available.
	vision string
	// Web looks up the brand and model an ad names, when the user turned that on.
	Web *websearch.Client
	// webOn is whether to look things up on the web during this scan.
	webOn bool
}

// maxPhotos is how many of a listing's photos the photo model looks at.
const maxPhotos = 4

// BaseURL overrides the marketplace address, for tests.
var BaseURL string

func (e *Engine) client(site market.Site) *market.Client {
	return &market.Client{Site: site, BaseURL: BaseURL}
}

func (e *Engine) delay() time.Duration {
	if e.Delay == 0 {
		return 2500 * time.Millisecond
	}
	return e.Delay
}

// sleep waits d or until ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// ScanResult summarises one pass over all watches.
type ScanResult struct {
	Fetched   int
	Evaluated int
	Alerts    int
	Errors    int
}

// Scan runs one pass over every enabled watch.
func (e *Engine) Scan(ctx context.Context) (ScanResult, error) {
	watches, err := watch.All()
	if err != nil {
		return ScanResult{}, err
	}
	e.searches = map[string][]market.Listing{}
	defer func() { e.searches = nil }()
	e.vision = ""
	if m := e.Config.VisionModel; m != "" {
		if e.LLM.Has(ctx, m) {
			e.vision = m
		} else {
			log.Printf("photo model %s is not downloaded; judging on text only", m)
		}
	}
	// Read from disk each scan, so switching it in the browser takes effect
	// without restarting the alerts.
	e.webOn = false
	if cfg, err := config.Load(); err == nil && cfg.WebLookup {
		e.webOn = true
		if e.Web == nil {
			e.Web = &websearch.Client{}
		}
	}
	var total ScanResult
	for _, w := range watches {
		if !w.Enabled {
			continue
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		res, err := e.scanWatch(ctx, w)
		total.Fetched += res.Fetched
		total.Evaluated += res.Evaluated
		total.Alerts += res.Alerts
		total.Errors += res.Errors
		if err != nil {
			log.Printf("watch %s: %v", w.Name, err)
			total.Errors++
		}
	}
	return total, nil
}

type found struct {
	listing market.Listing
	client  *market.Client
}

func (e *Engine) scanWatch(ctx context.Context, w watch.Watch) (ScanResult, error) {
	var res ScanResult
	var all []found
	seen := map[string]bool{}
	for _, site := range e.sitesFor(w) {
		client := e.client(site)
		listings, err := e.search(ctx, client, w)
		if err != nil {
			return res, fmt.Errorf("%s: %w", site.ID, err)
		}
		for _, l := range listings {
			if !seen[l.ItemID] {
				seen[l.ItemID] = true
				all = append(all, found{l, client})
			}
		}
	}
	res.Fetched = len(all)

	// The first time a watch runs, what is already listed is queued as a backlog
	// rather than judged at once: judging takes a detail page and a model call per
	// listing, and a new watch can find a hundred. Searching all sites first keeps
	// a failure on the second site from leaving it unqueued.
	count, err := e.Store.CountForWatch(w.Name)
	if err != nil {
		return res, err
	}
	if count == 0 {
		for _, f := range all {
			if err := e.Store.Record(row(w, f.listing, store.StatusSeeded, pendingReason, 0, 0, "")); err != nil {
				return res, err
			}
		}
		if len(all) > 0 {
			log.Printf("watch %s: %d listings already online, judging them %d per scan", w.Name, len(all), backlogPerScan)
		}
	}

	median := medianPrice(all)
	var backlog []found
	for _, f := range all {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		existing, err := e.Store.Get(w.Name, f.listing.ItemID)
		if err != nil {
			return res, err
		}
		if existing != nil && existing.Status == store.StatusSeeded {
			backlog = append(backlog, f)
			continue
		}
		if err := e.consider(ctx, f.client, w, f.listing, median(f.listing.ItemID), &res); err != nil {
			return res, err
		}
	}
	return res, e.judgeBacklog(ctx, w, backlog, median, &res)
}

const (
	// backlogPerScan is how many already-listed listings are judged per watch and
	// scan, so new listings are not held up behind them.
	backlogPerScan = 15
	// backlogAlerts is how many of those may alert per scan, best first. The rest
	// are shown in the browser interface.
	backlogAlerts = 3
)

// pendingReason is stored on listings that were already online when a watch was
// created, until they are judged.
const pendingReason = "Already listed when this search was created. Waiting to be judged; a few are judged every scan."

// judgeBacklog judges a batch of already-listed listings and alerts only the best.
func (e *Engine) judgeBacklog(ctx context.Context, w watch.Watch, backlog []found, median func(string) float64, res *ScanResult) error {
	if len(backlog) > backlogPerScan {
		backlog = backlog[:backlogPerScan]
	}
	// All photos are described before any listing is judged, so the photo model
	// and the text model, which do not fit in video memory together, swap once
	// per batch rather than twice per listing.
	var ready []prepared
	for _, f := range backlog {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p, err := e.prepare(ctx, f.client, w, f.listing, res)
		if err != nil {
			return err
		}
		if p != nil {
			ready = append(ready, *p)
		}
	}
	var judged []verdict
	for _, p := range ready {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v, err := e.finish(ctx, w, p, median(p.listing.ItemID), res)
		if err != nil {
			return err
		}
		if v != nil {
			judged = append(judged, *v)
		}
	}
	sort.SliceStable(judged, func(i, j int) bool {
		a, b := judged[i].deal, judged[j].deal
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.LearningScore > b.LearningScore
	})
	sent := 0
	for _, v := range judged {
		allowed := sent < backlogAlerts
		alerted, err := e.settle(w, v, allowed, res)
		if err != nil {
			return err
		}
		if alerted {
			sent++
		}
	}
	return nil
}

// medianPrice returns a function giving the median fixed price of the other
// listings in the same results, the going rate a buy-mode listing is compared to.
func medianPrice(all []found) func(itemID string) float64 {
	return func(itemID string) float64 {
		var prices []float64
		for _, f := range all {
			l := f.listing
			if l.ItemID != itemID && l.PriceType == "FIXED" && l.PriceCents > 0 {
				prices = append(prices, l.PriceEUR())
			}
		}
		if len(prices) < 3 {
			return 0
		}
		return score.Median(prices)
	}
}

func (e *Engine) sitesFor(w watch.Watch) []market.Site {
	want := map[string]bool{}
	for _, id := range w.Sites {
		want[id] = true
	}
	if len(want) == 0 {
		for _, id := range e.Config.Sites {
			want[id] = true
		}
	}
	var out []market.Site
	for _, s := range market.Sites() {
		if want[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func (e *Engine) search(ctx context.Context, client *market.Client, w watch.Watch) ([]market.Listing, error) {
	l1 := 0
	if cat, ok := client.Site.L1ByName(w.Category); ok {
		l1 = cat.ID
	}
	postcode, radius := w.Postcode, w.RadiusM
	if postcode == "" {
		postcode = e.Config.Postcode
	}
	if radius == 0 {
		radius = e.Config.DistanceM
	}
	var queries []market.Query
	if w.NotWorking && l1 > 0 {
		queries = append(queries, market.Query{L1Category: l1, Attributes: []string{"condition:" + notWorking},
			Postcode: postcode, DistanceM: radius, Limit: browseLimit})
	}
	if w.BrowseSubcategories && l1 > 0 && len(w.Subcategories) > 0 {
		queries = append(queries, market.Query{L1Category: l1, L2Categories: w.Subcategories,
			Postcode: postcode, DistanceM: radius, Limit: browseLimit})
	}
	var l2 []int
	if w.OnlySubcategories {
		l2 = w.Subcategories
	}
	for _, q := range w.Queries {
		queries = append(queries, market.Query{L1Category: l1, L2Categories: l2, Keyword: q,
			Postcode: postcode, DistanceM: radius, Limit: keywordLimit})
	}

	var out []market.Listing
	seen := map[string]bool{}
	for _, q := range queries {
		batch, err := e.cachedSearch(ctx, client, q)
		if err != nil {
			return nil, err
		}
		for _, l := range batch {
			if !seen[l.ItemID] {
				seen[l.ItemID] = true
				out = append(out, l)
			}
		}
	}
	return out, nil
}

func (e *Engine) cachedSearch(ctx context.Context, client *market.Client, q market.Query) ([]market.Listing, error) {
	key := fmt.Sprintf("%s|%+v", client.Site.ID, q)
	if hit, ok := e.searches[key]; ok {
		return hit, nil
	}
	batch, err := client.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	if e.searches != nil {
		e.searches[key] = batch
	}
	return batch, sleep(ctx, e.delay())
}

func (e *Engine) consider(ctx context.Context, client *market.Client, w watch.Watch, l market.Listing, median float64, res *ScanResult) error {
	existing, err := e.Store.Get(w.Name, l.ItemID)
	if err != nil {
		return err
	}
	if existing != nil {
		priceDropped := l.PriceType == "FIXED" && l.PriceCents < existing.PriceCents
		retry := existing.Status == store.StatusError && existing.Retries < 3
		if existing.Notified || !(priceDropped || retry) {
			return e.Store.Touch(w.Name, l.ItemID)
		}
	}
	v, err := e.judge(ctx, client, w, l, median, res)
	if err != nil || v == nil {
		return err
	}
	_, err = e.settle(w, *v, true, res)
	return err
}

// verdict is a judged listing that still needs its alert decision.
type verdict struct {
	listing market.Listing
	ev      score.Eval
	deal    score.Deal
}

// judge filters, fetches and evaluates one listing. It returns nil when the
// listing was skipped or failed; that outcome is already recorded.
func (e *Engine) judge(ctx context.Context, client *market.Client, w watch.Watch, l market.Listing, median float64, res *ScanResult) (*verdict, error) {
	p, err := e.prepare(ctx, client, w, l, res)
	if err != nil || p == nil {
		return nil, err
	}
	return e.finish(ctx, w, *p, median, res)
}

// prepared is a listing with its details fetched and its photos described.
type prepared struct {
	listing market.Listing
	photos  int
	seen    string
	// model is the brand and model the ad names, and web what the web says
	// about it, when it was looked up.
	model string
	web   string
}

// prepare filters a listing, fetches its details and describes its photos. It
// returns nil when the listing was skipped or failed; that outcome is recorded.
func (e *Engine) prepare(ctx context.Context, client *market.Client, w watch.Watch, l market.Listing, res *ScanResult) (*prepared, error) {
	if reason := e.prefilter(w, l); reason != "" {
		return nil, e.Store.Record(row(w, l, store.StatusSkipped, reason, 0, 0, ""))
	}
	if err := client.FetchDetails(ctx, &l); err != nil {
		log.Printf("details %s: %v", l.ItemID, err)
		res.Errors++
		return nil, e.recordError(w, l, err)
	}
	if err := sleep(ctx, e.delay()); err != nil {
		return nil, err
	}
	// The detail page has the full description, which can reveal a reject phrase
	// the search snippet cut off.
	if reason := e.prefilter(w, l); reason != "" {
		return nil, e.Store.Record(row(w, l, store.StatusSkipped, reason, 0, 0, ""))
	}
	p := &prepared{listing: l}
	if e.vision != "" && w.Mode == watch.ModeBuy && len(w.Requirements()) > 0 {
		if photos := client.FetchPhotos(ctx, l, maxPhotos); len(photos) > 0 {
			seen, err := e.describePhotos(ctx, photos)
			if err != nil {
				log.Printf("describe photos %s: %v", l.ItemID, err)
			} else if seen != "" {
				p.photos, p.seen = len(photos), seen
			}
		}
	}
	return p, nil
}

// finish evaluates a prepared listing.
func (e *Engine) finish(ctx context.Context, w watch.Watch, p prepared, median float64, res *ScanResult) (*verdict, error) {
	if e.webOn && w.Mode == watch.ModeBuy && len(w.Requirements()) > 0 {
		p.model, p.web = e.lookup(ctx, p.listing)
	}
	ev, deal, err := e.evaluate(ctx, w, p, median)
	if err != nil {
		log.Printf("evaluate %s: %v", p.listing.ItemID, err)
		res.Errors++
		return nil, e.recordError(w, p.listing, err)
	}
	res.Evaluated++
	return &verdict{listing: p.listing, ev: ev, deal: deal}, nil
}

// settle sends the alert when the score calls for one and mayAlert allows it, and
// records the outcome. It reports whether an alert was sent.
func (e *Engine) settle(w watch.Watch, v verdict, mayAlert bool, res *ScanResult) (bool, error) {
	l, ev, deal := v.listing, v.ev, v.deal
	raw, _ := json.Marshal(judgement{
		Eval: ev, Notes: deal.Notes, Questions: deal.Questions,
		RequirementsMet: deal.RequirementsMet, RequirementCount: deal.Requirements, MedianEUR: deal.MedianEUR,
	})
	wants := e.shouldAlert(w, deal)
	sent := false
	var sendErr error
	if wants && mayAlert && e.Bot != nil && e.ChatID != 0 {
		alert := telegram.Alert{Watch: w, Listing: l, Eval: ev, Deal: deal, DealAlert: deal.Score >= e.dealThreshold(w)}
		if sendErr = e.send(alert); sendErr != nil {
			log.Printf("alert %s: %v", l.ItemID, sendErr)
		} else {
			sent = true
			res.Alerts++
		}
	}
	decision := e.decisionText(w, deal, sent, sendErr)
	if wants && !mayAlert {
		decision = fmt.Sprintf("Not alerted, though deal score %d is a match: it was already listed when this search was created, and only the best %d of those are alerted per scan.", deal.Score, backlogAlerts)
	}
	if err := e.Store.Record(row(w, l, store.StatusEvaluated, decision, deal.Score, deal.LearningScore, string(raw))); err != nil {
		return false, err
	}
	log.Printf("watch %s: %s %q score %d learning %d", w.Name, l.ItemID, l.Title, deal.Score, deal.LearningScore)
	if sent {
		return true, e.Store.MarkNotified(w.Name, l.ItemID)
	}
	return false, nil
}

// judgement is what is stored for a judged listing: the model's answer, plus the
// notes the scoring added.
type judgement struct {
	score.Eval
	Notes            []string `json:"notes,omitempty"`
	Questions        []string `json:"questions,omitempty"`
	RequirementsMet  int      `json:"requirements_met,omitempty"`
	RequirementCount int      `json:"requirement_count,omitempty"`
	MedianEUR        float64  `json:"median_eur,omitempty"`
}

// decisionText says whether an alert was sent, and why.
func (e *Engine) decisionText(w watch.Watch, d score.Deal, sent bool, sendErr error) string {
	need := e.dealThreshold(w)
	switch {
	case sendErr != nil:
		return "Not alerted: sending the Telegram message failed. " + clip(sendErr.Error(), 180)
	case sent && w.Mode == watch.ModeRepair && d.Score < need:
		return fmt.Sprintf("Alerted as a learning project. Learning score %d, deal score %d.", d.LearningScore, d.Score)
	case sent:
		return fmt.Sprintf("Alerted. Deal score %d.", d.Score)
	case (e.Bot == nil || e.ChatID == 0) && e.shouldAlert(w, d):
		return fmt.Sprintf("Not alerted: deal score %d would alert, but Telegram is not connected.", d.Score)
	case w.Mode == watch.ModeRepair && e.Config.MinLearningScore > 0:
		return fmt.Sprintf("Not alerted. Deal score %d is below %d and learning score %d is below %d.",
			d.Score, need, d.LearningScore, e.Config.MinLearningScore)
	default:
		return fmt.Sprintf("Not alerted. Deal score %d is below %d.", d.Score, need)
	}
}

func (e *Engine) prefilter(w watch.Watch, l market.Listing) string {
	if !w.Belongs(l.Title, l.CategoryID) {
		return "not this watch's kind of item"
	}
	rules := prefilter.Rules{
		MaxPriceEUR: w.MaxPrice, RejectPhrases: w.RejectPhrases,
		WantedTerms: prefilter.DefaultWanted, NegativePhrases: prefilter.DefaultNegatives,
	}
	if w.Mode == watch.ModeRepair {
		words := append([]string{}, prefilter.DefaultFaultWords...)
		rules.FaultWords = append(words, w.FaultWords...)
		rules.RequireFault = true
		rules.ConditionFault = notWorking
	}
	return prefilter.Reason(prefilter.Listing{
		Title: l.Title, Description: l.Description, PriceEUR: l.PriceEUR(),
		PriceType: l.PriceType, Condition: l.Condition,
	}, rules)
}

func (e *Engine) evaluate(ctx context.Context, w watch.Watch, p prepared, median float64) (score.Eval, score.Deal, error) {
	l := p.listing
	text := fmt.Sprintf("Title: %s\nPrice: %s\nCondition: %s\nDelivery: %s\nDescription:\n%s",
		l.Title, l.PriceLabel(), orDefault(l.Condition, "unknown"), orDefault(l.Delivery, "unknown"), clip(l.Description, 3000))
	facts := score.ListingFacts{PriceEUR: l.PriceEUR(), PriceType: l.PriceType, HighestBid: float64(l.HighestBid) / 100}

	if w.Mode == watch.ModeBuy {
		ev, err := e.evaluateBuy(ctx, w, text, p)
		if err != nil {
			return ev, score.Deal{}, err
		}
		var hard []bool
		if w.Description != "" {
			hard = append(hard, true)
		}
		for _, r := range w.Requirements() {
			hard = append(hard, r.Hard)
		}
		deal := score.Buy(facts, ev, score.BuyParams{Hard: hard, MedianEUR: median})
		if ev.LikeRejected && deal.Score > 1 {
			deal.Score = max(1, deal.Score-2)
			deal.Notes = append(deal.Notes, "resembles a listing you marked not relevant")
		}
		return ev, deal, nil
	}

	ev, err := e.LLM.Evaluate(ctx, e.Config.Model, w.Prompt, text, e.Options)
	if err != nil {
		return ev, score.Deal{}, err
	}
	params := score.DefaultRepair()
	if len(w.AllowedFaults) > 0 {
		params.AllowedFaults = map[string]bool{}
		for _, f := range w.AllowedFaults {
			params.AllowedFaults[f] = true
		}
	}
	return ev, score.Repair(facts, ev, params), nil
}

// evaluateBuy judges a listing against the buyer's requirements. The text model
// judges, from the ad and the description of its photos: it reads ads better
// than the photo model.
func (e *Engine) evaluateBuy(ctx context.Context, w watch.Watch, text string, p prepared) (score.Eval, error) {
	seen := p.seen
	reqs := w.Requirements()
	var list strings.Builder
	for i, r := range reqs {
		c := r.Check
		if c == nil || c.Kind == "" {
			fmt.Fprintf(&list, "%d. %s\n", i+1, r.Name)
		} else {
			// The buyer's own words are often a sign rather than the requirement
			// ("rood-zwarte draad" for passive speakers), so the reading leads.
			fmt.Fprintf(&list, "%d. %s\n   The buyer wrote: %s\n", i+1, c.Kind, r.Name)
		}
		if c != nil {
			fmt.Fprintf(&list, "   Means: %s\n", c.Means)
			if len(c.Signs) > 0 {
				fmt.Fprintf(&list, "   Shown by words like: %s\n", strings.Join(c.Signs, ", "))
			}
			if len(c.Against) > 0 {
				fmt.Fprintf(&list, "   Ruled out by: %s\n", strings.Join(c.Against, ", "))
			}
			if c.Photo != "" && seen != "" {
				fmt.Fprintf(&list, "   On a photo: %s\n", c.Photo)
			}
		}
	}
	bases := []string{score.BasisText, "none"}
	photoRule := ""
	user := text
	if seen != "" {
		bases = []string{score.BasisText, score.BasisPhoto, "none"}
		photoRule = `
- "photo": the description of the photos shows it. Copy its exact words into quote and say in reason what they show.`
		user = text + "\n\nThe photos of the item, as described by someone who did not know the requirements:\n" + seen
	}
	webRule := ""
	if p.web != "" {
		bases = append(bases[:len(bases)-1], score.BasisWeb, "none")
		webRule = fmt.Sprintf(`
- "web": what web pages say about the %s shows it, for example its specifications ("recommended amplifier power" shows a speaker is passive). Copy the page's exact words into quote and say in reason what they show. Only for this exact model.`, p.model)
		user += fmt.Sprintf("\n\nWhat web pages say about the %s:\n%s", p.model, p.web)
	}
	system := `You check a classified ad against a buyer's requirements. Sellers rarely use the buyer's words, so look for what each requirement means.
Return exactly one verdict per requirement, in the order given, with its number. First name the evidence (basis), then decide:
- "ad text": the title or description shows it, in so many words or by what it says the item is ("werkt op batterijen" shows there is no power cord). Copy the seller's exact words into quote.` + photoRule + webRule + `
- "none": nothing shows it either way.
The quote must be copied from the source named in basis; the words of the requirements themselves are never evidence.
Status "met" when the evidence shows it, "unmet" when the evidence shows the item does not have it, otherwise "unknown". That the ad does not mention it is not evidence, and neither is what is typical for this kind of item.
Requirements:
` + list.String()
	// Property order in the answer follows the JSON keys, which Go sorts: the model
	// writes kind_offered (what the ad sells) before deciding only_part_or_accessory,
	// and the basis and evidence of a requirement before its status.
	properties := map[string]any{
		"requirements": map[string]any{
			"type":     "array",
			"minItems": len(reqs),
			"maxItems": len(reqs),
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"number": map[string]any{"type": "integer"},
					"basis":  map[string]any{"type": "string", "enum": bases},
					"quote":  map[string]any{"type": "string", "maxLength": 200},
					"reason": map[string]any{"type": "string", "maxLength": 200},
					"status": map[string]any{"type": "string", "enum": []string{"met", "unmet", "unknown"}},
				},
				"required": []string{"number", "basis", "quote", "reason", "status"},
			},
		},
	}
	required := []string{"requirements"}
	if w.Description != "" {
		system += fmt.Sprintf(`
The buyer wants: %s
In kind_offered, copy the words from the ad's title that name what is being sold.
Then set only_part_or_accessory to true when the ad sells only a spare part, loose parts or an accessory for that item instead of the complete item itself.`, w.Description)
		properties["kind_offered"] = map[string]any{"type": "string", "maxLength": 80}
		properties["only_part_or_accessory"] = map[string]any{"type": "boolean"}
		required = append(required, "kind_offered", "only_part_or_accessory")
	}

	rejected, accepted := e.examples(w.Name)
	if len(rejected) > 0 {
		system += "\nThe buyer marked these earlier listings as not what they want:\n- " + strings.Join(rejected, "\n- ")
		if len(accepted) > 0 {
			system += "\nAnd these as relevant:\n- " + strings.Join(accepted, "\n- ")
		}
		system += "\nSet like_rejected to true only when this listing is clearly the same kind of item as a rejected one."
		properties["like_rejected"] = map[string]any{"type": "boolean"}
		required = append(required, "like_rejected")
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required}

	var answer struct {
		Requirements []struct {
			Number int    `json:"number"`
			Basis  string `json:"basis"`
			Quote  string `json:"quote"`
			Reason string `json:"reason"`
			Status string `json:"status"`
		} `json:"requirements"`
		LikeRejected bool   `json:"like_rejected"`
		KindOffered  string `json:"kind_offered"`
		OnlyPart     bool   `json:"only_part_or_accessory"`
	}
	if err := e.LLM.Chat(ctx, e.Config.Model, system, user, schema, &answer, e.Options); err != nil {
		return score.Eval{}, err
	}

	// Verdicts are matched to the watch's requirements by number, falling back to
	// position. The names always come from the watch, so a model that rephrases or
	// drops a requirement cannot make a hard requirement disappear.
	ev := score.Eval{Requirements: make([]score.Requirement, len(reqs)), LikeRejected: answer.LikeRejected, PhotosSeen: p.photos, Seen: seen}
	if p.web != "" {
		ev.LookedUp = p.model
	}
	for i, r := range reqs {
		ev.Requirements[i] = score.Requirement{Name: r.Name, Status: "unknown"}
	}
	for i, a := range answer.Requirements {
		idx := a.Number - 1
		if idx < 0 || idx >= len(reqs) {
			idx = i
		}
		if idx >= len(reqs) {
			continue
		}
		if a.Basis == "none" {
			continue
		}
		switch a.Status {
		case "met", "unmet":
			r := &ev.Requirements[idx]
			r.Status, r.Quote = a.Status, a.Quote
			if a.Basis == score.BasisPhoto || a.Basis == score.BasisWeb {
				r.Basis, r.Reason = a.Basis, strings.TrimSpace(a.Reason)
			} else {
				r.Basis = score.BasisText
			}
		}
	}
	score.GroundBuy(&ev, text, map[string]string{score.BasisPhoto: seen, score.BasisWeb: p.web})

	// The kind of item is mostly settled by the watch's title words; small models are
	// only reliable at spotting the parts and accessories that share those words. It
	// is a judgement, not a quote, so it is added after grounding.
	if w.Description != "" {
		item := score.Requirement{Name: watch.ItemRequirement, Status: "met", Quote: clip(answer.KindOffered, 80)}
		if answer.OnlyPart {
			item.Status = "unmet"
		}
		ev.Requirements = append([]score.Requirement{item}, ev.Requirements...)
	}
	return ev, nil
}

const describeSystem = `You describe the photos of a second-hand item for sale, for a buyer who cannot see them. Write in English, factually, in short lines:
- what the item is, and how many;
- any brand, model or other text printed on the item or its labels, copied exactly;
- every connection that is visible: sockets, terminals, clips, plugs, cables and power cords, with their kind and colours, and where they are;
- buttons, displays, antennas or batteries;
- damage or wear.
Describe only what you can see. Do not guess about parts that are not in view.`

// describePhotos says what the photos of a listing show. It is asked before the
// model knows what to look for: told "red and black terminals", small photo
// models see them on every speaker.
func (e *Engine) describePhotos(ctx context.Context, photos [][]byte) (string, error) {
	opt := e.Options
	opt.NumCtx = max(opt.NumCtx, 8192)
	opt.NumPredict = max(opt.NumPredict, 500)
	model := e.vision
	user := fmt.Sprintf("%d photos of the item are attached.", len(photos))
	var answer struct {
		Description string `json:"description"`
	}
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"description": map[string]any{"type": "string", "maxLength": 1500}},
		"required":   []string{"description"},
	}
	if err := e.LLM.ChatImages(ctx, model, describeSystem, user, photos, schema, &answer, opt); err != nil {
		return "", err
	}
	return unrepeat(answer.Description), nil
}

// unrepeat drops the phrases a model already wrote: small photo models can loop
// on one ("4-way crossover, 4-way crossover, ...").
func unrepeat(s string) string {
	seen := map[string]bool{}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		var kept []string
		for _, part := range strings.Split(line, ", ") {
			key := strings.ToLower(strings.TrimRight(strings.TrimSpace(part), ".;"))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			kept = append(kept, strings.TrimSpace(part))
		}
		if len(kept) > 0 {
			b.WriteString(strings.Join(kept, ", ") + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// examples returns recent titles the user marked not relevant and relevant.
func (e *Engine) examples(watchName string) (rejected, accepted []string) {
	feedback, err := e.Store.FeedbackFor(watchName, 30)
	if err != nil {
		return nil, nil
	}
	for _, f := range feedback {
		if f.Title == "" {
			continue
		}
		switch {
		case f.Verdict == store.NotRelevant && len(rejected) < 5:
			rejected = append(rejected, clip(f.Title, 100))
		case f.Verdict == store.Relevant && len(accepted) < 5:
			accepted = append(accepted, clip(f.Title, 100))
		}
	}
	return rejected, accepted
}

func (e *Engine) dealThreshold(w watch.Watch) int {
	if w.MinScore > 0 {
		return w.MinScore
	}
	return e.Config.MinDealScore
}

func (e *Engine) shouldAlert(w watch.Watch, d score.Deal) bool {
	if d.Score >= e.dealThreshold(w) {
		return true
	}
	return w.Mode == watch.ModeRepair && e.Config.MinLearningScore > 0 && d.LearningScore >= e.Config.MinLearningScore
}

func (e *Engine) send(a telegram.Alert) error {
	caption := telegram.Format(a)
	buttons := telegram.Buttons(a)
	if a.Listing.ImageURL != "" {
		return e.Bot.SendPhoto(e.ChatID, a.Listing.ImageURL, caption, buttons)
	}
	return e.Bot.Send(e.ChatID, caption, buttons, true)
}

func (e *Engine) recordError(w watch.Watch, l market.Listing, cause error) error {
	existing, err := e.Store.Get(w.Name, l.ItemID)
	if err != nil {
		return err
	}
	if existing == nil {
		if err := e.Store.Record(row(w, l, store.StatusError, cause.Error(), 0, 0, "")); err != nil {
			return err
		}
	}
	return e.Store.AddRetry(w.Name, l.ItemID, clip(cause.Error(), 500))
}

func row(w watch.Watch, l market.Listing, status, reason string, sc, learn int, evalJSON string) store.Row {
	return store.Row{
		Watch: w.Name, ItemID: l.ItemID, Title: l.Title, PriceCents: l.PriceCents, PriceType: l.PriceType,
		URL: l.URL, Site: l.Site, Status: status, Reason: reason, Score: sc, LearningScore: learn, EvalJSON: evalJSON,
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Run scans until ctx is cancelled, waiting the configured interval plus jitter.
// It also answers Telegram commands and alert buttons in the background.
func (e *Engine) Run(ctx context.Context) error {
	if e.Bot != nil && e.ChatID != 0 {
		go e.listen(ctx)
	}
	for {
		res, err := e.Scan(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("scan: %v", err)
		}
		log.Printf("scan done: fetched %d, evaluated %d, alerts %d, errors %d", res.Fetched, res.Evaluated, res.Alerts, res.Errors)
		wait := time.Duration(e.Config.PollS)*time.Second + time.Duration(rand.Intn(45))*time.Second
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

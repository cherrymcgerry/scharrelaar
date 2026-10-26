package watch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/prefilter"
)

// Searcher is the part of the marketplace client the interview uses.
type Searcher interface {
	SearchPage(ctx context.Context, q market.Query) (market.Page, error)
}

// Explorer asks one marketplace how sellers list an item.
type Explorer struct {
	Search Searcher
	Site   market.Site
	// Delay between requests; the interview makes about ten.
	Delay time.Duration
}

func (x Explorer) page(ctx context.Context, q market.Query) (market.Page, error) {
	page, err := x.Search.SearchPage(ctx, q)
	if err == nil && x.Delay > 0 {
		select {
		case <-ctx.Done():
			return page, ctx.Err()
		case <-time.After(x.Delay):
		}
	}
	return page, err
}

// NameCount is how the listings that use one name spread over the subcategories.
type NameCount struct {
	Name          string
	Subcategories []market.CategoryCount
	// Mined is set for names taken from listing titles; the ones that do not fit
	// are dropped without a note.
	Mined bool
}

func (n NameCount) total() int {
	sum := 0
	for _, c := range n.Subcategories {
		sum += c.Count
	}
	return sum
}

func (n NameCount) in(ids []int) int {
	sum := 0
	for _, c := range n.Subcategories {
		for _, id := range ids {
			if c.ID == id {
				sum += c.Count
			}
		}
	}
	return sum
}

// Option is a subcategory the buyer can choose, with the listings of all names
// in it added up.
type Option struct {
	market.CategoryCount
	L1 market.Category
	// Parts is set for subcategories that sell parts of the item.
	Parts bool
}

// Label names the option the way the site's menus do.
func (o Option) Label() string { return o.L1.Name + " › " + o.Name }

// Findings is what the marketplace says about the names.
type Findings struct {
	Names   []NameCount
	Options []Option
}

// maxOptions keeps the subcategory question short; smaller ones rarely matter.
const maxOptions = 6

// Explore looks up where each name is listed, nationwide.
func (x Explorer) Explore(ctx context.Context, names []string) (Findings, error) {
	var f Findings
	sums := map[int]*Option{}
	for _, name := range names {
		page, err := x.page(ctx, market.Query{Keyword: name, Limit: 1})
		if err != nil {
			return Findings{}, err
		}
		f.Names = append(f.Names, NameCount{Name: name, Subcategories: page.Subcategories})
		for _, c := range page.Subcategories {
			if o, ok := sums[c.ID]; ok {
				o.Count += c.Count
				continue
			}
			l1, ok := x.Site.L1ByID(c.ParentID)
			if !ok {
				continue
			}
			sums[c.ID] = &Option{CategoryCount: c, L1: l1, Parts: partsRE.MatchString(c.Name)}
		}
	}
	for _, o := range sums {
		f.Options = append(f.Options, *o)
	}
	sort.Slice(f.Options, func(i, j int) bool { return f.Options[i].Count > f.Options[j].Count })
	if len(f.Options) > 0 {
		cut := f.Options[0].Count / 20
		kept := f.Options[:0]
		for _, o := range f.Options {
			if o.Count > cut && len(kept) < maxOptions {
				kept = append(kept, o)
			}
		}
		f.Options = kept
	}
	return f, nil
}

// SuggestOptions has the model pick the subcategories where the item itself is
// sold. It returns their ids; on any failure it falls back to the largest one
// that does not sell parts.
func SuggestOptions(ctx context.Context, m *llm.Manager, model, description string, options []Option, opt llm.Options) []int {
	wantsParts := partsRE.MatchString(description)
	fallback := func() []int {
		for _, o := range options {
			if !o.Parts || wantsParts {
				return []int{o.ID}
			}
		}
		return nil
	}
	if len(options) < 2 {
		return fallback()
	}
	var list strings.Builder
	for i, o := range options {
		fmt.Fprintf(&list, "%d. %s\n", i+1, o.Label())
	}
	system := fmt.Sprintf(`A buyer on a classifieds site wants: %s
The user sends the numbered subcategories where ads that mention it are listed. Reply with the numbers of the subcategories where the complete item the buyer wants is sold. Leave out subcategories for parts, accessories or a different kind of item, unless the buyer asks for those.`, description)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"numbers": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 1, "maxItems": len(options)},
		},
		"required": []string{"numbers"},
	}
	var answer struct {
		Numbers []int `json:"numbers"`
	}
	if err := m.Chat(ctx, model, system, list.String(), schema, &answer, opt); err != nil {
		return fallback()
	}
	var ids []int
	seen := map[int]bool{}
	for _, n := range answer.Numbers {
		if n < 1 || n > len(options) || seen[n] {
			continue
		}
		seen[n] = true
		if options[n-1].Parts && !wantsParts {
			continue
		}
		ids = append(ids, options[n-1].ID)
	}
	if len(ids) == 0 {
		return fallback()
	}
	return ids
}

// Plan is what the chosen subcategories hold.
type Plan struct {
	L1            market.Category
	Subcategories []Option
	// Total is the number of listings in the subcategories nationwide; Nearby is a
	// sample within the radius and NearbyTotal their number.
	Total       int
	Nearby      []market.Listing
	NearbyTotal int
	// Browse is set when the subcategories hold mostly this kind of item, so they
	// are searched as a whole. Otherwise Queries are searched inside them.
	Browse  bool
	Queries []string
	// Names are the names sellers in these subcategories use for the item.
	Names []string
	Notes []string
}

func (p Plan) ids() []int {
	ids := make([]int, len(p.Subcategories))
	for i, o := range p.Subcategories {
		ids[i] = o.ID
	}
	return ids
}

// Where describes the subcategories for display.
func (p Plan) Where() string {
	names := make([]string, len(p.Subcategories))
	for i, o := range p.Subcategories {
		names[i] = o.Name
	}
	return p.L1.Name + " › " + strings.Join(names, ", ")
}

// browseShare is how much of a subcategory one name must cover before the whole
// subcategory is searched: "aanhangwagen" is in 45% of "Aanhangers en
// Bagagewagens", a tool name in a few percent of "Gereedschap".
const browseShare = 0.35

// maxQueries limits the keyword searches per scan.
const maxQueries = 4

// Inspect looks at the chosen subcategories. Watches search one top-level
// category, so subcategories of another one than the first are left out.
func (x Explorer) Inspect(ctx context.Context, f Findings, chosen []int, postcode string, radius int) (Plan, error) {
	var p Plan
	for _, id := range chosen {
		for _, o := range f.Options {
			if o.ID != id {
				continue
			}
			if len(p.Subcategories) == 0 {
				p.L1 = o.L1
			}
			if o.L1.ID != p.L1.ID {
				p.Notes = append(p.Notes, fmt.Sprintf("left out %s: a search covers one top-level category", o.Label()))
				continue
			}
			p.Subcategories = append(p.Subcategories, o)
		}
	}
	if len(p.Subcategories) == 0 {
		return p, fmt.Errorf("no subcategory chosen")
	}
	ids := p.ids()
	national, err := x.page(ctx, market.Query{L1Category: p.L1.ID, L2Categories: ids, Limit: 1})
	if err != nil {
		return p, err
	}
	p.Total = national.Total

	// A word taken from titles that is far more common than the buyer's own names
	// is broader than the item ("zaag" for a decoupeerzaag).
	own := 0
	for _, n := range f.Names {
		if !n.Mined {
			own = max(own, n.in(ids))
		}
	}
	best := 0
	names := append([]NameCount(nil), f.Names...)
	sort.SliceStable(names, func(i, j int) bool { return names[i].in(ids) > names[j].in(ids) })
	for _, n := range names {
		in, all := n.in(ids), n.total()
		if n.Mined && in > 2*own {
			continue
		}
		best = max(best, in)
		switch {
		case all == 0:
			if !n.Mined {
				p.Notes = append(p.Notes, fmt.Sprintf("not used %q: no listing uses it", n.Name))
			}
		case in*10 < all:
			if !n.Mined {
				p.Notes = append(p.Notes, fmt.Sprintf("not used %q: sellers use it mostly for %s", n.Name, n.Subcategories[0].Name))
			}
		case in >= 3:
			p.Names = append(p.Names, n.Name)
			if len(p.Queries) < maxQueries {
				p.Queries = append(p.Queries, n.Name)
			}
		}
	}
	p.Browse = p.Total > 0 && float64(best) >= browseShare*float64(p.Total)

	// The sample shows what the search will see: the whole subcategory when it is
	// browsed, otherwise the listings that use the best name.
	nearbyQuery := market.Query{L1Category: p.L1.ID, L2Categories: ids, Postcode: postcode, DistanceM: radius, Limit: 100}
	if !p.Browse && len(p.Queries) > 0 {
		nearbyQuery.Keyword = p.Queries[0]
	}
	nearby, err := x.page(ctx, nearbyQuery)
	if err != nil {
		return p, err
	}
	p.Nearby, p.NearbyTotal = nearby.Listings, nearby.Total
	return p, nil
}

// AddNames looks up more names and adds them to the findings, leaving the
// subcategory options as they are.
func (x Explorer) AddNames(ctx context.Context, f *Findings, names []string) error {
	for _, name := range names {
		known := false
		for _, n := range f.Names {
			known = known || n.Name == name
		}
		if known {
			continue
		}
		page, err := x.page(ctx, market.Query{Keyword: name, Limit: 1})
		if err != nil {
			return err
		}
		f.Names = append(f.Names, NameCount{Name: name, Subcategories: page.Subcategories, Mined: true})
	}
	return nil
}

// stopwords are frequent title words that say nothing about the kind of item.
var stopwords = map[string]bool{
	"voor": true, "met": true, "zonder": true, "en": true, "van": true, "de": true, "het": true, "een": true,
	"nieuw": true, "nieuwe": true, "goede": true, "staat": true, "prima": true, "mooie": true, "prachtige": true,
	"koop": true, "kopen": true, "verkoop": true, "aangeboden": true, "gebruikt": true, "perfecte": true,
	"leverbaar": true, "voorraad": true, "stock": true, "alle": true, "type": true, "merk": true, "zeer": true,
	"veiling": true, "pour": true, "avec": true, "with": true, "the": true, "and": true, "for": true,
}

// TitleWords returns the words used most in the listings' titles, apart from the
// names already known.
func TitleWords(listings []market.Listing, names []string, n int) []string {
	counts := map[string]int{}
	for _, l := range listings {
		seen := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(l.Title), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 0xC0 && r <= 0x24F)
		}) {
			if utf8.RuneCountInString(w) < 4 || stopwords[w] || seen[w] || prefilter.HasAnyTerm(w, names) {
				continue
			}
			seen[w] = true
			counts[w]++
		}
	}
	var words []string
	for w, c := range counts {
		if c >= 2 {
			words = append(words, w)
		}
	}
	sort.Slice(words, func(i, j int) bool {
		if counts[words[i]] != counts[words[j]] {
			return counts[words[i]] > counts[words[j]]
		}
		return words[i] < words[j]
	})
	if len(words) > n {
		words = words[:n]
	}
	return words
}

// PriceRange returns the lower and upper quartile of the asking prices, when
// enough listings have one.
func PriceRange(listings []market.Listing) (low, high float64, ok bool) {
	var prices []float64
	for _, l := range listings {
		if (l.PriceType == "FIXED" || l.PriceType == "MIN_BID") && l.PriceCents > 100 {
			prices = append(prices, l.PriceEUR())
		}
	}
	if len(prices) < 5 {
		return 0, 0, false
	}
	sort.Float64s(prices)
	return prices[len(prices)/4], prices[len(prices)*3/4], true
}

// Question is something open in the request, with answers taken from listings.
// Every option the buyer ticks becomes a requirement.
type Question struct {
	Text    string
	Options []string
	// Selected are the options ticked from the start.
	Selected []string
}

// Synonyms has the model pick, from words in real titles, the other names sellers
// use for the item: "remork" and "bagagewagen" for an aanhangwagen. Choosing from
// a list is something small models do far better than recalling the words, but
// they also pick brands and properties; Labels.Synonyms removes those.
func Synonyms(ctx context.Context, m *llm.Manager, model, description, item string, words []string, opt llm.Options) ([]string, error) {
	if len(words) == 0 {
		return nil, nil
	}
	system := fmt.Sprintf(`A buyer on a classifieds site wants: %s
The user sends words that appear in the titles of ads in the matching category. Which of those words are other names for the same kind of item as "%s": a synonym, a translation, a dialect word or another spelling? Leave out kinds, sizes, brands, parts and properties.`,
		description, item)
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"names": stringList(0, 8)},
		"required":   []string{"names"},
	}
	var answer struct {
		Names []string `json:"names"`
	}
	if err := m.Chat(ctx, model, system, strings.Join(words, ", "), schema, &answer, opt); err != nil {
		return nil, err
	}
	var out []string
	for _, n := range answer.Names {
		n = strings.ToLower(strings.TrimSpace(n))
		if inAny(n, words) && !inAny(n, out) {
			out = append(out, n)
		}
	}
	return out, nil
}

// wordKinds are the labels the model gives title words.
var wordKinds = []string{"name", "type", "size", "feature", "material", "brand", "model", "colour", "condition", "other"}

// Labels sorts the words sellers use in titles.
type Labels struct {
	// Names are other names for the item: synonyms, translations, spellings.
	Names []string
	// Choices are kinds, variants, features, sizes and materials: what a buyer may
	// want the item to be or have.
	Choices []string
	// Not are words that cannot be a name for the item: brands, models, colours,
	// conditions and properties.
	Not []string
	// Stated are kinds and features the buyer named that are not a requirement yet
	// ("gesloten" in "een gesloten aanhangwagen"). They are offered ticked.
	Stated []string
}

// Synonyms keeps the proposed synonyms that were not labelled as something else.
func (l Labels) Synonyms(proposed []string) []string {
	var out []string
	for _, s := range proposed {
		if !inAny(s, l.Not) {
			out = append(out, s)
		}
	}
	return out
}

// Question asks which kinds or features the item must have, starting with the
// ones the buyer named, or returns false when there is nothing to ask.
func (l Labels) Question(synonyms []string) (Question, bool) {
	var options []string
	for _, c := range l.Choices {
		if !inAny(c, synonyms) && !inAny(c, l.Stated) {
			options = append(options, c)
		}
	}
	options = append(append([]string{}, l.Stated...), firstN(options, 10)...)
	if len(options) == 0 {
		return Question{}, false
	}
	return Question{Text: "Must it be or have any of these?", Options: options, Selected: l.Stated}, true
}

// Label has the model say what each title word tells about the item. Labelling
// a given list is reliable even for small models, where recalling synonyms or
// writing good questions is not. Words the buyer already used are not offered
// again; those that are a kind or feature become Stated.
func Label(ctx context.Context, m *llm.Manager, model, description string, in Intent, words []string, opt llm.Options) (Labels, error) {
	reqs := strings.Join(append(append([]string{in.Item}, singleWords(in.Names)...), append(in.MustHave, in.NiceToHave...)...), " ")
	var open []string
	said := openWords(description, in)
	for _, w := range words {
		switch {
		case prefilter.HasCompoundTerm(reqs, w):
		case prefilter.HasCompoundTerm(description, w):
			if !inAny(w, said) {
				said = append(said, w)
			}
		default:
			open = append(open, w)
		}
	}
	all := append(append([]string{}, open...), said...)
	if len(all) == 0 {
		return Labels{}, nil
	}
	system := fmt.Sprintf(`A buyer is looking for this on a classifieds site: %s
The user sends words used most in the titles of matching ads. Label every one of those words with what it tells about the item:
- name: another name for the item itself (%s): a synonym, a translation, a dialect word or another spelling
- type: a kind or variant of the item
- size: a size, capacity or dimension
- feature: something the item can have or do
- material: what it is made of
- brand, model, colour or condition
- other: anything else, or nothing about the item`, description, in.Item)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"labels": map[string]any{
				"type": "array", "minItems": len(all), "maxItems": len(all),
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"word": map[string]any{"type": "string"},
						"kind": map[string]any{"type": "string", "enum": wordKinds},
					},
					"required": []string{"word", "kind"},
				},
			},
		},
		"required": []string{"labels"},
	}
	var answer struct {
		Labels []struct {
			Word string `json:"word"`
			Kind string `json:"kind"`
		} `json:"labels"`
	}
	if err := m.Chat(ctx, model, system, strings.Join(all, ", "), schema, &answer, opt); err != nil {
		return Labels{}, err
	}
	kinds := map[string]string{}
	for _, a := range answer.Labels {
		w := strings.ToLower(strings.TrimSpace(a.Word))
		if _, dup := kinds[w]; !dup {
			kinds[w] = a.Kind
		}
	}
	// open is ordered by how often sellers use the word; the labels keep that order.
	var l Labels
	for _, w := range open {
		kind, ok := kinds[w]
		if !ok {
			continue
		}
		if w == "huur" || w == "verhuur" {
			kind = "other"
		}
		switch kind {
		case "name":
			l.Names = append(l.Names, w)
		case "type":
			l.Choices = append(l.Choices, w)
		case "size", "feature", "material":
			l.Choices = append(l.Choices, w)
			l.Not = append(l.Not, w)
		default:
			l.Not = append(l.Not, w)
		}
	}
	for _, w := range said {
		switch kinds[w] {
		case "type", "size", "feature", "material":
			l.Stated = append(l.Stated, w)
		}
	}
	return l, nil
}

func firstN(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// Build turns the interview into a watch. plan is nil when the marketplace had no
// listings for any of the names; the names are then searched as they are.
func Build(description string, in Intent, plan *Plan) Draft {
	d := Draft{Restated: in.Summary, Plan: plan}
	w := Watch{
		Enabled: true, Mode: in.Mode, Description: strings.TrimSpace(description),
		MaxPrice: in.MaxPrice, RadiusM: in.RadiusM, Postcode: in.Postcode,
		MustHave: in.MustHave, NiceToHave: in.NiceToHave, Exclude: defaultExclude,
	}
	if plan == nil {
		w.Queries = in.Names
		if len(w.Queries) > maxQueries {
			w.Queries = w.Queries[:maxQueries]
		}
		w.TitleTerms = in.Names
		d.Notes = append(d.Notes, "the marketplace has no listings for these words right now, so they are searched everywhere")
	} else {
		w.Category = plan.L1.Name
		w.Subcategories = plan.ids()
		d.Where = plan.Where()
		if plan.Browse || len(plan.Queries) == 0 {
			w.BrowseSubcategories = true
		} else {
			w.OnlySubcategories = true
			w.Queries = plan.Queries
		}
		d.Notes = append(d.Notes, plan.Notes...)
	}
	w.Name = SuggestName([]string{in.Item})
	d.Watch = w
	return d
}

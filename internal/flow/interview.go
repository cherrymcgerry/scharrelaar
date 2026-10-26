package flow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

// Interview turns a description into a draft: the model reads it, the marketplace
// says where sellers list the item, and the user settles what is open.
func Interview(ctx context.Context, cfg config.Config, mgr *llm.Manager, description string, a Asker) (watch.Draft, error) {
	return interview(ctx, cfg, mgr, description, a, nil)
}

func interview(ctx context.Context, cfg config.Config, mgr *llm.Manager, description string, a Asker, search watch.Searcher) (watch.Draft, error) {
	// Only working out what the wishes mean needs a thinking model to think; the
	// other steps read and sort, and the buyer is waiting.
	opt := llm.Options{NumCtx: 4096, NumPredict: 800, NoThink: true}
	a.Progress("Reading your request...")
	in, err := watch.Understand(ctx, mgr, cfg.Model, description, opt)
	if err != nil {
		return watch.Draft{}, err
	}
	site, ok := market.SiteByID(firstOr(cfg.Sites, "2dehands"))
	if !ok {
		return watch.Draft{}, fmt.Errorf("unknown site %q", firstOr(cfg.Sites, "2dehands"))
	}
	delay := time.Second
	if search == nil {
		search = &market.Client{Site: site}
	} else {
		delay = 0
	}
	x := watch.Explorer{Search: search, Site: site, Delay: delay}
	a.Progress(fmt.Sprintf("Checking how sellers on %s list it (%s)...", site.Name, strings.Join(in.Names, ", ")))
	f, err := x.Explore(ctx, in.Names)
	if err != nil {
		return watch.Draft{}, err
	}

	if in.Place != "" && in.Postcode == "" {
		if in.Postcode, err = askPostcode(a, in.Place); err != nil {
			return watch.Draft{}, err
		}
	}
	postcode, radius := cfg.Postcode, cfg.DistanceM
	if in.Postcode != "" {
		postcode = in.Postcode
	}
	if in.RadiusM > 0 {
		radius = in.RadiusM
	}
	var plan *watch.Plan
	var labels watch.Labels
	var synonyms []string
	if len(f.Options) > 0 {
		chosen := watch.SuggestOptions(ctx, mgr, cfg.Model, description, f.Options, opt)
		if len(f.Options) > 1 {
			if chosen, err = askSubcategories(a, f.Options, chosen); err != nil {
				return watch.Draft{}, err
			}
		}
		p, err := x.Inspect(ctx, f, chosen, postcode, radius)
		if err != nil {
			return watch.Draft{}, err
		}
		words := watch.TitleWords(p.Nearby, in.Names, 30)
		if labels, err = watch.Label(ctx, mgr, cfg.Model, description, in, words, opt); err != nil {
			a.Progress(fmt.Sprintf("(skipping follow-up questions: %v)", err))
		}
		proposed, err := watch.Synonyms(ctx, mgr, cfg.Model, description, in.Item, words, opt)
		if err != nil {
			a.Progress(fmt.Sprintf("(no synonyms from listings: %v)", err))
		}
		more := labels.Synonyms(proposed)
		synonyms = more
		// Browsing a subcategory finds every wording anyway; as keyword searches, a
		// wrong synonym would bring in other items, so the user confirms them.
		if len(more) > 0 && !p.Browse {
			if more, err = askSynonyms(a, in.Item, more); err != nil {
				return watch.Draft{}, err
			}
		}
		if len(more) > 0 {
			if err := x.AddNames(ctx, &f, more); err != nil {
				return watch.Draft{}, err
			}
			in.Names = append(in.Names, more...)
			if p, err = x.Inspect(ctx, f, chosen, postcode, radius); err != nil {
				return watch.Draft{}, err
			}
		}
		plan = &p
	}

	if in.MaxPrice == 0 {
		var listings []market.Listing
		if plan != nil {
			listings = plan.Nearby
		}
		if in.MaxPrice, err = askBudget(a, listings); err != nil {
			return watch.Draft{}, err
		}
	}
	if q, ok := labels.Question(synonyms); ok {
		answers, err := askQuestion(a, q)
		if err != nil {
			return watch.Draft{}, err
		}
		in.MustHave = append(in.MustHave, answers...)
	}
	draft := watch.Build(description, in, plan)
	wishes := append(append([]string{}, draft.Watch.MustHave...), draft.Watch.NiceToHave...)
	if len(wishes) > 0 {
		a.Progress("Looking at how sellers describe what you asked for...")
		usage := map[string][]string{}
		for _, w := range wishes {
			usage[w] = x.Usage(ctx, plan, w)
		}
		a.Progress("Working out how to recognise it in an ad...")
		readings, err := watch.Interpret(ctx, mgr, cfg.Model, description, in.Item, wishes, usage, opt)
		if err != nil {
			a.Progress(fmt.Sprintf("(checking your requirements as written: %v)", err))
		}
		if draft.Watch.Checks, err = confirmChecks(ctx, a, cfg, mgr, description, in.Item, readings, usage, opt); err != nil {
			return watch.Draft{}, err
		}
	}
	return draft, nil
}

// confirmChecks lets the buyer pick what each wish means, explain it, or keep it
// as written.
func confirmChecks(ctx context.Context, a Asker, cfg config.Config, mgr *llm.Manager, description, item string, readings []watch.Readings, usage map[string][]string, opt llm.Options) ([]watch.Check, error) {
	var out []watch.Check
	for _, r := range readings {
		var opts []Option
		var details []string
		for i, c := range r.Options {
			opts = append(opts, Option{Label: c.Title(), Value: strconv.Itoa(i), Selected: i == 0})
			details = append(details, c.Describe())
		}
		opts = append(opts,
			Option{Label: "Something else, let me explain", Value: "explain"},
			Option{Label: "Check it exactly as I wrote it", Value: "literal"})
		got, err := a.Choose(Choice{
			Title:       fmt.Sprintf("What do you mean by %q?", r.Wish),
			Description: "Sellers rarely write it your way, so listings are checked for what it means:\n\n" + strings.Join(details, "\n\n"),
			Options:     opts,
			Min:         1,
		})
		if err != nil {
			return nil, err
		}
		pick := firstOr(got, "0")
		if i, err := strconv.Atoi(pick); err == nil && i >= 0 && i < len(r.Options) {
			out = append(out, r.Options[i])
			continue
		}
		if pick != "explain" {
			continue
		}
		explained, err := a.Input(Field{
			Title:       fmt.Sprintf("What do you mean by %q?", r.Wish),
			Description: "For example what it has to work with, or what it looks like.",
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(explained) == "" {
			continue
		}
		wish := r.Wish + " (" + explained + ")"
		again, err := watch.Interpret(ctx, mgr, cfg.Model, description+"\n"+r.Wish+": "+explained, item, []string{wish}, map[string][]string{wish: usage[r.Wish]}, opt)
		if err != nil || len(again) == 0 {
			a.Progress(fmt.Sprintf("(checking %q as written)", r.Wish))
			continue
		}
		c := again[0].Options[0]
		c.Wish = r.Wish
		a.Progress(fmt.Sprintf("Understood %q as: %s", r.Wish, c.Title()))
		out = append(out, c)
	}
	return out, nil
}

func askSubcategories(a Asker, options []watch.Option, suggested []int) ([]int, error) {
	var opts []Option
	for _, o := range options {
		opts = append(opts, Option{
			Label:    fmt.Sprintf("%s (%d listings)", o.Label(), o.Count),
			Value:    strconv.Itoa(o.ID),
			Selected: containsInt(suggested, o.ID),
		})
	}
	chosen, err := a.Choose(Choice{
		Title:       "Where is it listed? Pick what you mean",
		Description: "Ticked categories are searched.",
		Options:     opts,
		Multiple:    true,
		Min:         1,
	})
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, c := range chosen {
		id, err := strconv.Atoi(c)
		if err != nil || !knownOption(options, id) {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("pick at least one category")
	}
	return ids, nil
}

func askPostcode(a Asker, place string) (string, error) {
	answer, err := a.Input(Field{
		Title:       fmt.Sprintf("What is the postcode of %s?", place),
		Description: "Leave empty to search around your own postcode.",
		Validate: func(s string) string {
			if strings.TrimSpace(s) != "" && watch.ValidPostcode(s) == "" {
				return "enter a postcode such as 8000 or 1011 AB"
			}
			return ""
		},
	})
	if err != nil {
		return "", err
	}
	return watch.ValidPostcode(answer), nil
}

func askBudget(a Asker, listings []market.Listing) (float64, error) {
	hint := "Leave empty for no maximum."
	if low, high, ok := watch.PriceRange(listings); ok {
		hint = fmt.Sprintf("Most listings near you ask between €%.0f and €%.0f. Leave empty for no maximum.", low, high)
	}
	answer, err := a.Input(Field{
		Title:       "Maximum price in euro?",
		Description: hint,
		Validate: func(s string) string {
			if s = cleanPrice(s); s == "" {
				return ""
			}
			if _, err := strconv.ParseFloat(s, 64); err != nil {
				return "enter a number, or leave it empty"
			}
			return ""
		},
	})
	if err != nil {
		return 0, err
	}
	answer = cleanPrice(answer)
	if answer == "" {
		return 0, nil
	}
	return strconv.ParseFloat(answer, 64)
}

func askSynonyms(a Asker, item string, names []string) ([]string, error) {
	var opts []Option
	for _, n := range names {
		opts = append(opts, Option{Label: n, Value: n})
	}
	return a.Choose(Choice{
		Title:       fmt.Sprintf("Sellers also use these words. Which mean the same as %q?", item),
		Description: "Ticked words are searched too.",
		Options:     opts,
		Multiple:    true,
	})
}

// askQuestion returns the options the user ticked plus anything they typed, each
// a requirement.
func askQuestion(a Asker, q watch.Question) ([]string, error) {
	var opts []Option
	for _, o := range q.Options {
		opts = append(opts, Option{Label: o, Value: o, Selected: contains(q.Selected, o)})
	}
	chosen, err := a.Choose(Choice{
		Title:       q.Text,
		Description: "Tick what it must be or have. Leave them all unticked if none apply.",
		Options:     opts,
		Multiple:    true,
	})
	if err != nil {
		return nil, err
	}
	extra, err := a.Input(Field{
		Title:       "Anything else it must have?",
		Description: "Optional, separate with commas.",
	})
	if err != nil {
		return nil, err
	}
	for _, e := range strings.Split(extra, ",") {
		if e = strings.TrimSpace(e); e != "" {
			chosen = append(chosen, e)
		}
	}
	return chosen, nil
}

func cleanPrice(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "€"))
	return strings.ReplaceAll(s, ",", ".")
}

func knownOption(options []watch.Option, id int) bool {
	for _, o := range options {
		if o.ID == id {
			return true
		}
	}
	return false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func firstOr(list []string, fallback string) string {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}

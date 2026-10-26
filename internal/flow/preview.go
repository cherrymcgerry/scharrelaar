package flow

import (
	"context"
	"fmt"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

// Example is one listing shown so the user can judge a draft.
type Example struct {
	Title string `json:"title"`
	Price string `json:"price"`
	URL   string `json:"url"`
}

// Preview describes what is listed right now for a draft. Text is a sentence;
// Examples are up to three listings. A failed lookup is reported in Text.
func Preview(ctx context.Context, cfg config.Config, d watch.Draft) (text string, examples []Example) {
	if d.Plan != nil {
		return previewPlan(d)
	}
	return previewSearch(ctx, cfg, d.Watch)
}

func previewPlan(d watch.Draft) (string, []Example) {
	p := d.Plan
	w := d.Watch
	text := fmt.Sprintf("%d listings are in there within your radius right now", p.NearbyTotal)
	var examples []Example
	for _, l := range p.Nearby {
		if len(examples) == 3 {
			break
		}
		if !w.Belongs(l.Title, l.CategoryID) || w.MaxPrice > 0 && l.PriceEUR() > w.MaxPrice && (l.PriceType == "FIXED" || l.PriceType == "MIN_BID") {
			continue
		}
		examples = append(examples, exampleFrom(l))
	}
	if len(examples) == 0 {
		return text + ".", nil
	}
	return text + ", for example:", examples
}

func previewSearch(ctx context.Context, cfg config.Config, w watch.Watch) (string, []Example) {
	if len(cfg.Sites) == 0 || len(w.Queries) == 0 {
		return "", nil
	}
	site, ok := market.SiteByID(cfg.Sites[0])
	if !ok {
		return "", nil
	}
	client := &market.Client{Site: site}
	l1 := 0
	if cat, ok := site.L1ByName(w.Category); ok {
		l1 = cat.ID
	}
	postcode, radius := cfg.Postcode, cfg.DistanceM
	if w.Postcode != "" {
		postcode = w.Postcode
	}
	if w.RadiusM > 0 {
		radius = w.RadiusM
	}
	listings, err := client.Search(ctx, market.Query{
		L1Category: l1, Keyword: w.Queries[0], Postcode: postcode, DistanceM: radius, Limit: 3,
	})
	if err != nil {
		return fmt.Sprintf("(preview unavailable: %v)", err), nil
	}
	if len(listings) == 0 {
		return "No current listings match this search.", nil
	}
	var examples []Example
	for _, l := range listings {
		examples = append(examples, exampleFrom(l))
	}
	return "Currently listed:", examples
}

func exampleFrom(l market.Listing) Example {
	return Example{Title: l.Title, Price: l.PriceLabel(), URL: l.URL}
}

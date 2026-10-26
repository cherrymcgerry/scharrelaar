package cli

import (
	"context"
	"fmt"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
)

// Doctor checks that each configured marketplace still answers in the shape the
// app expects, and reports the first thing that is wrong.
func Doctor(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sites := cfg.Sites
	if len(sites) == 0 {
		sites = []string{"2dehands", "marktplaats"}
	}
	problems := 0
	for _, id := range sites {
		site, ok := market.SiteByID(id)
		if !ok {
			fmt.Printf("%s: unknown site\n", id)
			problems++
			continue
		}
		client := &market.Client{Site: site}
		listings, err := client.Search(ctx, market.Query{Keyword: "fiets", Limit: 1})
		if err != nil {
			fmt.Printf("%s: search failed: %v\n", site.Name, err)
			problems++
			continue
		}
		if len(listings) == 0 || listings[0].ItemID == "" || listings[0].URL == "" {
			fmt.Printf("%s: search returned no usable listing (the site may have changed its API)\n", site.Name)
			problems++
			continue
		}
		fmt.Printf("%s: search ok (%s)\n", site.Name, listings[0].ItemID)
	}
	if problems > 0 {
		return fmt.Errorf("%d check(s) failed", problems)
	}
	fmt.Println("All checks passed.")
	return nil
}

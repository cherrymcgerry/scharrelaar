package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestParseSearch(t *testing.T) {
	body, err := os.ReadFile("../../testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	listings, err := ParseSearch(body, TwoDeHands)
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) == 0 {
		t.Fatal("no listings parsed")
	}
	l := listings[0]
	if !strings.HasPrefix(l.ItemID, "m") {
		t.Errorf("item id %q", l.ItemID)
	}
	if !strings.HasPrefix(l.URL, "https://www.2dehands.be/v/") {
		t.Errorf("url %q", l.URL)
	}
	if l.PriceType == "" {
		t.Error("empty price type")
	}
	if l.Condition == "" {
		t.Error("condition attribute not parsed")
	}
	if l.Site != "2dehands" {
		t.Errorf("site %q", l.Site)
	}
	if _, ok := TwoDeHands.L1ByKey(l.L1Key); !ok {
		t.Errorf("category key %q from %s not resolved", l.L1Key, l.URL)
	}
}

func TestParsePageCounts(t *testing.T) {
	body, err := os.ReadFile("../../testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	page, err := ParsePage(body, TwoDeHands)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total == 0 || len(page.Subcategories) == 0 {
		t.Fatalf("total %d, %d subcategories", page.Total, len(page.Subcategories))
	}
	for i, c := range page.Subcategories {
		if c.ID == 0 || c.ParentID == 0 || c.Name == "" || c.Count == 0 {
			t.Errorf("incomplete %+v", c)
		}
		if i > 0 && c.Count > page.Subcategories[i-1].Count {
			t.Error("not sorted by count")
		}
	}
}

func TestSearchSubcategories(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write([]byte(`{"listings":[]}`))
	}))
	defer srv.Close()
	c := &Client{Site: TwoDeHands, BaseURL: srv.URL}
	if _, err := c.Search(context.Background(), Query{L1Category: 48, L2Categories: []int{49, 1134}}); err != nil {
		t.Fatal(err)
	}
	if ids := got["l2CategoryIds"]; len(ids) != 2 || ids[0] != "49" || ids[1] != "1134" {
		t.Errorf("l2CategoryIds %v", ids)
	}
}

func TestCategoryKeys(t *testing.T) {
	for key, want := range map[string]int{"auto-diversen": 48, "auto-s": 91, "cd-s-en-dvd-s": 1744, "kleding-dames": 621, "elektronische-apparatuur": 537} {
		if c, ok := TwoDeHands.L1ByKey(key); !ok || c.ID != want {
			t.Errorf("%s: %+v %v", key, c, ok)
		}
	}
	if c, ok := Marktplaats.L1ByKey("witgoed-en-apparatuur"); !ok || c.ID != 537 {
		t.Errorf("marktplaats witgoed: %+v %v", c, ok)
	}
	if c, ok := Marktplaats.L1ByName("Elektronische apparatuur"); !ok || c.Name != "Witgoed en Apparatuur" {
		t.Errorf("cross-site name: %+v %v", c, ok)
	}
	if got := l1Key("/v/auto-diversen/aanhangers/m1-titel"); got != "auto-diversen" {
		t.Errorf("l1Key %q", got)
	}
}

func TestCategoriesFixture(t *testing.T) {
	body, err := os.ReadFile("../../testdata/categories.json")
	if err != nil {
		t.Fatal(err)
	}
	listings, err := ParseSearch(body, TwoDeHands)
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) != 1 {
		t.Fatalf("got %d listings", len(listings))
	}
	var resp struct {
		Options []Category `json:"searchCategoryOptions"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Options) < 10 {
		t.Fatalf("only %d subcategories", len(resp.Options))
	}
	found := false
	for _, c := range resp.Options {
		if c.ParentID == 1847 && c.Name != "" && c.ID != 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("no subcategory of 1847")
	}
}

func TestL1Overlap(t *testing.T) {
	byID := map[int]string{}
	for _, c := range TwoDeHands.L1 {
		byID[c.ID] = c.Name
	}
	shared := 0
	for _, c := range Marktplaats.L1 {
		if byID[c.ID] != "" {
			shared++
		}
	}
	if shared < 30 {
		t.Errorf("only %d shared L1 ids", shared)
	}
	if _, ok := TwoDeHands.L1ByName("audio, tv en foto"); !ok {
		t.Error("case-insensitive lookup failed")
	}
}

func TestPriceLabel(t *testing.T) {
	l := Listing{PriceCents: 1500, PriceType: "MIN_BID"}
	if got := l.PriceLabel(); got != "bieden vanaf €15" {
		t.Errorf("got %q", got)
	}
	l.PriceType = "FREE"
	if got := l.PriceLabel(); got != "gratis" {
		t.Errorf("got %q", got)
	}
}

func TestExtractConfig(t *testing.T) {
	html := `<html><script>window.__CONFIG__ = {"listing":{"title":"Test","priceInfo":{"priceCents":65000,"priceType":"FIXED"},"bidsInfo":{"bids":[{"value":1000},{"value":2500}]},"gallery":{"imageUrls":["//img/a$_#.jpg"]}}};</script>`
	cfg, ok := extractConfig(html)
	if !ok {
		t.Fatal("config not found")
	}
	l := &Listing{PriceType: "UNKNOWN", DistanceM: -1}
	applyConfig(l, cfg)
	if l.Title != "Test" || l.PriceCents != 65000 || l.PriceType != "FIXED" {
		t.Errorf("parsed %+v", l)
	}
	if l.BidCount != 2 || l.HighestBid != 2500 {
		t.Errorf("bids %d highest %d", l.BidCount, l.HighestBid)
	}
	if !strings.HasPrefix(l.ImageURL, "https://img/a") {
		t.Errorf("image %q", l.ImageURL)
	}
}

func TestStripTags(t *testing.T) {
	got := stripTags("Hallo<br>wereld &amp; co")
	if got != "Hallo\nwereld & co" {
		t.Errorf("got %q", got)
	}
}

package watch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
)

// fakeModel answers every chat with the same JSON.
func fakeModel(t *testing.T, answer any) {
	t.Helper()
	raw, _ := json.Marshal(answer)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]any{"message": map[string]string{"content": string(raw)}})
		w.Write(resp)
	}))
	t.Cleanup(srv.Close)
	llm.HostOverride = srv.URL
	t.Cleanup(func() { llm.HostOverride = "" })
}

func TestUnderstand(t *testing.T) {
	fakeModel(t, map[string]any{
		"item": "aanhangwagen", "names": []string{"Aanhangwagen", "aanhanger", "trailer tot 900 euro", "te koop", "remorque", "aanhangwagen voor de auto"},
		"max_price_eur": 900, "radius_m": 60000, "place": "Gent", "postcode": "9000", "must_have": []string{"geremd", "aanhangwagen", "max 900 euro"},
		"nice_to_have": []string{"met dekzeil"}, "mode": "repair", "summary": "Een geremde aanhangwagen tot 900 euro bij Gent.",
	})
	in, err := Understand(context.Background(), &llm.Manager{}, "test",
		"geremde aanhangwagen tot 900 euro, binnen 60 km van Gent, liefst met dekzeil", llm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if in.Mode != ModeBuy {
		t.Errorf("mode %s for a plain purchase", in.Mode)
	}
	if got := strings.Join(in.Names, "|"); got != "aanhangwagen|aanhanger|trailer|remorque" {
		t.Errorf("names %q", got)
	}
	if got := strings.Join(in.MustHave, "|"); got != "geremd" {
		t.Errorf("must have %q", got)
	}
	if in.Postcode != "9000" || in.MaxPrice != 900 || in.RadiusM != 60000 {
		t.Errorf("intent %+v", in)
	}
}

func TestUnderstandDropsWhatWasNotSaid(t *testing.T) {
	fakeModel(t, map[string]any{
		"item": "kinderfiets", "names": []string{"kinderfiets"}, "max_price_eur": 100, "radius_m": 20000,
		"place": "", "postcode": "9000", "must_have": []string{"van 6 jaar"}, "nice_to_have": []string{"kleurrijk", "colorful"},
		"mode": "buy", "summary": "",
	})
	in, err := Understand(context.Background(), &llm.Manager{}, "test", "een kinderfiets voor mijn dochter van 6", llm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if in.Postcode != "" || in.RadiusM != 0 || len(in.NiceToHave) != 0 || len(in.MustHave) != 1 {
		t.Errorf("invented details kept: %+v", in)
	}
	if in.MaxPrice != 100 {
		t.Error("a number in the description may be a budget")
	}
}

func TestOpenWords(t *testing.T) {
	in := Intent{Item: "aanhangwagen", Names: []string{"aanhangwagen", "trailer"}, MustHave: []string{"minstens 2,5 m lang"}, Place: "Brugge"}
	got := openWords("Ik zoek een geremde aanhangwagen rond Brugge, minstens 2,5 m lang, max 900 euro", in)
	if strings.Join(got, ",") != "geremde" {
		t.Errorf("open words %v", got)
	}
	in = Intent{Item: "aanhangwagen", Names: []string{"aanhangwagen", "gesloten aanhangwagen"}}
	if got := openWords("een gesloten aanhangwagen", in); strings.Join(got, ",") != "gesloten" {
		t.Errorf("a property inside a name counts as the item: %v", got)
	}
}

func TestRepairModeNeedsRepairWords(t *testing.T) {
	in := Intent{Mode: ModeRepair, Item: "versterker"}
	tidyIntent(&in, "kapotte versterkers om te herstellen")
	if in.Mode != ModeRepair {
		t.Error("repair request turned into buy")
	}
	if !foreignScript(map[string]any{"notes": "需要一个带刹车的汽车拖车"}) || foreignScript(map[string]any{"notes": "Écran cassé, één"}) {
		t.Error("script detection")
	}
}

// fakeSite serves the category counts measured on 2dehands for trailer words.
type fakeSite struct {
	keyword  map[string][]market.CategoryCount
	national int
	nearby   []market.Listing
	queries  []market.Query
}

func (f *fakeSite) SearchPage(_ context.Context, q market.Query) (market.Page, error) {
	f.queries = append(f.queries, q)
	if q.Keyword != "" {
		return market.Page{Subcategories: f.keyword[q.Keyword]}, nil
	}
	if q.Postcode != "" {
		return market.Page{Total: len(f.nearby), Listings: f.nearby}, nil
	}
	return market.Page{Total: f.national}, nil
}

func cat(id, parent, count int, name string) market.CategoryCount {
	return market.CategoryCount{Category: market.Category{ID: id, ParentID: parent, Name: name}, Count: count}
}

func trailerSite() *fakeSite {
	return &fakeSite{
		keyword: map[string][]market.CategoryCount{
			"aanhangwagen": {cat(49, 48, 1678, "Aanhangers en Bagagewagens"), cat(1134, 48, 1082, "Aanhangwagen-onderdelen"), cat(450, 445, 558, "Fietsaccessoires | Aanhangwagens en Karren")},
			"bagagewagen":  {cat(49, 48, 2726, "Aanhangers en Bagagewagens"), cat(1100, 1099, 75, "Modeltreinen | H0")},
			"trailer":      {cat(414, 395, 400, "Paarden en Pony's | Trailers en Aanhangwagens"), cat(49, 48, 322, "Aanhangers en Bagagewagens"), cat(980, 976, 179, "Speedboten")},
			"trekwagen":    {cat(570, 565, 4, "Speelgoed | Houten speelgoed")},
			"karretje":     {},
		},
		national: 3717,
		nearby: []market.Listing{
			{Title: "Gesloten aanhangwagen 750 kg", PriceType: "FIXED", PriceCents: 120000, CategoryID: 49},
			{Title: "Kipper Hulco 2700 kg", PriceType: "FIXED", PriceCents: 350000, CategoryID: 49},
			{Title: "Kipper ongeremd", PriceType: "MIN_BID", PriceCents: 90000, CategoryID: 49},
			{Title: "Aanhangwagen gesloten met huif", PriceType: "FIXED", PriceCents: 80000, CategoryID: 49},
			{Title: "Bagagewagen ongeremd", PriceType: "FIXED", PriceCents: 45000, CategoryID: 49},
			{Title: "Autotransporter te huur", PriceType: "SEE_DESCRIPTION", CategoryID: 49},
		},
	}
}

func TestExploreAndInspect(t *testing.T) {
	site := trailerSite()
	x := Explorer{Search: site, Site: market.TwoDeHands}
	names := []string{"aanhangwagen", "bagagewagen", "trailer", "trekwagen", "karretje"}
	f, err := x.Explore(context.Background(), names)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Options) == 0 || f.Options[0].ID != 49 || f.Options[0].Count != 1678+2726+322 {
		t.Fatalf("options %+v", f.Options)
	}
	for _, o := range f.Options {
		if o.ID == 1134 && !o.Parts {
			t.Error("parts subcategory not marked")
		}
		if o.ID == 570 {
			t.Error("a subcategory with 4 listings is not worth asking about")
		}
	}
	if got := f.Options[0].Label(); got != "Auto diversen › Aanhangers en Bagagewagens" {
		t.Errorf("label %q", got)
	}

	p, err := x.Inspect(context.Background(), f, []int{49, 414}, "9000", 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Browse || p.L1.ID != 48 || len(p.Subcategories) != 1 {
		t.Errorf("plan %+v", p)
	}
	if got := strings.Join(p.Queries, "|"); got != "bagagewagen|aanhangwagen|trailer" {
		t.Errorf("queries %q", got)
	}
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{"left out Dieren en Toebehoren", `"trekwagen": sellers use it mostly for Speelgoed`, `"karretje": no listing`} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes missing %q:\n%s", want, notes)
		}
	}
	last := site.queries[len(site.queries)-1]
	if last.L1Category != 48 || len(last.L2Categories) != 1 || last.L2Categories[0] != 49 || last.Postcode != "9000" {
		t.Errorf("nearby query %+v", last)
	}

	d := Build("aanhangwagen", Intent{Item: "aanhangwagen", Mode: ModeBuy, Summary: "Een aanhangwagen."}, &p)
	w := d.Watch
	if !w.BrowseSubcategories || w.Category != "Auto diversen" || len(w.Subcategories) != 1 || len(w.Queries) != 0 {
		t.Errorf("watch %+v", w)
	}
	if err := w.Validate(); err != nil {
		t.Error(err)
	}
	if !w.Belongs("Kipper Hulco", 49) || w.Belongs("Autotransporter te huur", 49) || w.Belongs("Fietskar", 450) {
		t.Error("routing of the browsed subcategory")
	}
	if !strings.Contains(d.Summary(), "Searches: every listing in Auto diversen › Aanhangers en Bagagewagens") {
		t.Errorf("summary:\n%s", d.Summary())
	}
}

func TestBroadSubcategoryUsesKeywords(t *testing.T) {
	site := trailerSite()
	site.national = 100000
	x := Explorer{Search: site, Site: market.TwoDeHands}
	f, _ := x.Explore(context.Background(), []string{"aanhangwagen", "bagagewagen"})
	p, err := x.Inspect(context.Background(), f, []int{49}, "9000", 100000)
	if err != nil {
		t.Fatal(err)
	}
	if p.Browse {
		t.Error("browsing a subcategory the names barely cover")
	}
	w := Build("aanhangwagen", Intent{Item: "aanhangwagen", Mode: ModeBuy}, &p).Watch
	if !w.OnlySubcategories || strings.Join(w.Queries, "|") != "bagagewagen|aanhangwagen" {
		t.Errorf("watch %+v", w)
	}
}

func TestBuildWithoutListings(t *testing.T) {
	d := Build("x", Intent{Item: "zeldzaam ding", Names: []string{"zeldzaam ding", "rare thing"}, Mode: ModeBuy}, nil)
	if d.Watch.Name != "zeldzaam-ding" || len(d.Watch.Queries) != 2 || len(d.Watch.TitleTerms) != 2 || len(d.Notes) != 1 {
		t.Errorf("draft %+v", d)
	}
}

func TestSuggestOptionsSkipsParts(t *testing.T) {
	options := []Option{
		{CategoryCount: cat(1134, 48, 10406, "Aanhangwagen-onderdelen"), Parts: true},
		{CategoryCount: cat(49, 48, 4635, "Aanhangers en Bagagewagens")},
	}
	fakeModel(t, map[string]any{"numbers": []int{1, 2, 7}})
	if got := SuggestOptions(context.Background(), &llm.Manager{}, "test", "een aanhangwagen", options, llm.Options{}); len(got) != 1 || got[0] != 49 {
		t.Errorf("got %v", got)
	}
	if got := SuggestOptions(context.Background(), &llm.Manager{}, "test", "onderdelen voor mijn aanhangwagen", options, llm.Options{}); len(got) != 2 {
		t.Errorf("parts wanted, got %v", got)
	}
}

func TestQuestionsFromLabels(t *testing.T) {
	site := trailerSite()
	words := TitleWords(site.nearby, []string{"aanhangwagen"}, 30)
	if strings.Join(words, ",") != "gesloten,kipper,ongeremd" {
		t.Errorf("words %v", words)
	}
	fakeModel(t, map[string]any{"labels": []map[string]string{
		{"word": "gesloten", "kind": "type"}, {"word": "Kipper", "kind": "type"}, {"word": "kipper", "kind": "type"},
		{"word": "ongeremd", "kind": "feature"}, {"word": "plateau", "kind": "type"}, {"word": "hulco", "kind": "brand"},
		{"word": "remork", "kind": "name"},
	}})
	in := Intent{Item: "aanhangwagen"}
	l, err := Label(context.Background(), &llm.Manager{}, "test", "een aanhangwagen", in, append(words, "hulco", "remork"), llm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Names, ",") != "remork" {
		t.Errorf("names %v", l.Names)
	}
	synonyms := l.Synonyms([]string{"remork", "hulco", "kipper"})
	if strings.Join(synonyms, ",") != "remork,kipper" {
		t.Errorf("a brand passed as a synonym: %v", synonyms)
	}
	q, ok := l.Question(synonyms)
	if !ok || strings.Join(q.Options, ",") != "gesloten,ongeremd" {
		t.Errorf("question %+v", q)
	}

	l, _ = Label(context.Background(), &llm.Manager{}, "test", "een gesloten aanhangwagen", in, words, llm.Options{})
	if q, _ := l.Question(nil); strings.Join(q.Options, ",") != "gesloten,kipper,ongeremd" || strings.Join(q.Selected, ",") != "gesloten" {
		t.Errorf("the buyer's own kind is not offered ticked: %+v", q)
	}
	in.MustHave = []string{"gesloten"}
	if l, _ = Label(context.Background(), &llm.Manager{}, "test", "een gesloten aanhangwagen", in, words, llm.Options{}); len(l.Stated) != 0 {
		t.Errorf("requirement added twice: %v", l.Stated)
	}
}

func TestPriceRange(t *testing.T) {
	low, high, ok := PriceRange(trailerSite().nearby)
	if !ok || low != 800 || high != 1200 {
		t.Errorf("range %v-%v %v", low, high, ok)
	}
	if _, _, ok := PriceRange(nil); ok {
		t.Error("range from nothing")
	}
}

func TestValidPostcode(t *testing.T) {
	for in, want := range map[string]string{"9000": "9000", "1011 AB": "1011 AB", "Gent": "", "0123": "", "": ""} {
		if got := ValidPostcode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSuggestName(t *testing.T) {
	if got := SuggestName([]string{"Aanhangwagen geremd 750kg"}); got != "aanhangwagen-geremd" {
		t.Errorf("got %q", got)
	}
	if got := SuggestName(nil); got != "watch" {
		t.Errorf("got %q", got)
	}
}

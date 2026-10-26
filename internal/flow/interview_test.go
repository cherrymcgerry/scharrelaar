package flow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

// script answers questions from a table and records what it was asked.
type script struct {
	mu     sync.Mutex
	inputs map[string]string
	asks   []string
	link   string
}

func (s *script) Progress(msg string) {
	s.mu.Lock()
	s.asks = append(s.asks, "progress:"+msg)
	s.mu.Unlock()
}

func (s *script) Show(c Card) {
	s.mu.Lock()
	s.asks = append(s.asks, "show:"+c.Title)
	if c.QR != "" {
		s.link = c.QR
	}
	s.mu.Unlock()
}

func (s *script) Choose(q Choice) ([]string, error) {
	s.mu.Lock()
	s.asks = append(s.asks, "choose:"+q.Title)
	s.mu.Unlock()
	var out []string
	for _, o := range q.Options {
		if o.Selected {
			out = append(out, o.Value)
		}
	}
	if len(out) == 0 && len(q.Options) > 0 {
		out = []string{q.Options[0].Value}
	}
	if !q.Multiple && len(out) > 1 {
		out = out[:1]
	}
	return out, nil
}

func (s *script) Input(q Field) (string, error) {
	s.mu.Lock()
	s.asks = append(s.asks, "input:"+q.Title)
	answer, ok := s.inputs[q.Title]
	s.mu.Unlock()
	if !ok {
		if q.Default != "" {
			return q.Default, nil
		}
		return "", errNoAnswer(q.Title)
	}
	if q.Validate != nil {
		if msg := q.Validate(answer); msg != "" {
			return "", errNoAnswer(msg)
		}
	}
	return answer, nil
}

type answerError string

func (e answerError) Error() string { return string(e) }

func errNoAnswer(title string) error { return answerError("no scripted answer for " + title) }

func (s *script) asked(part string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.asks {
		if strings.Contains(a, part) {
			return true
		}
	}
	return false
}

func TestInterviewAsksAndBuilds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		system := ""
		if len(req.Messages) > 0 {
			system = req.Messages[0].Content
		}
		var answer any
		switch {
		case strings.Contains(system, "names: 3 to 8"):
			answer = map[string]any{
				"item": "aanhangwagen", "names": []string{"aanhangwagen", "aanhanger", "trailer"},
				"max_price_eur": 0, "radius_m": 0, "place": "", "postcode": "",
				"must_have": []string{}, "nice_to_have": []string{}, "mode": "buy",
				"summary": "Een aanhangwagen.",
			}
		case strings.Contains(system, "numbered subcategories"):
			answer = map[string]any{"numbers": []int{1}}
		case strings.Contains(system, "Label every one"):
			answer = map[string]any{"labels": []any{}}
		default:
			answer = map[string]any{"names": []string{}}
		}
		raw, _ := json.Marshal(answer)
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": string(raw)}})
	}))
	defer srv.Close()

	ask := &script{inputs: map[string]string{"Maximum price in euro?": "900"}}
	cfg := config.Config{Model: "test", Sites: []string{"2dehands"}, Postcode: "9000", DistanceM: 100000}
	draft, err := interview(context.Background(), cfg, &llm.Manager{HostURL: srv.URL}, "een aanhangwagen", ask, trailerSite())
	if err != nil {
		t.Fatal(err)
	}
	if !ask.asked("Where is it listed?") {
		t.Errorf("subcategories were not asked: %v", ask.asks)
	}
	if !ask.asked("Maximum price") {
		t.Errorf("budget was not asked: %v", ask.asks)
	}
	w := draft.Watch
	if w.MaxPrice != 900 || w.Category != "Auto diversen" || !w.BrowseSubcategories {
		t.Errorf("watch %+v", w)
	}
	if w.Name != "aanhangwagen" || w.Mode != watch.ModeBuy {
		t.Errorf("name %q mode %q", w.Name, w.Mode)
	}
	if err := w.Validate(); err != nil {
		t.Error(err)
	}
}

func TestPreviewPlan(t *testing.T) {
	d := watch.Draft{
		Watch: watch.Watch{MaxPrice: 1000, Subcategories: []int{49}, Mode: watch.ModeBuy, TitleTerms: []string{"aanhangwagen"}},
		Plan: &watch.Plan{
			NearbyTotal: 4,
			Nearby: []market.Listing{
				{Title: "Aanhangwagen", PriceType: "FIXED", PriceCents: 50000, CategoryID: 49, URL: "https://example.test/a"},
				{Title: "Something else", PriceType: "FIXED", PriceCents: 1000, CategoryID: 99},
			},
		},
	}
	text, examples := Preview(context.Background(), config.Config{}, d)
	if !strings.Contains(text, "4 listings") || len(examples) != 1 || examples[0].URL != "https://example.test/a" {
		t.Fatalf("%q %+v", text, examples)
	}
}

// trailerSite serves the category counts measured on 2dehands for trailer words.
type trailerSearch struct {
	keyword map[string][]market.CategoryCount
}

func (f *trailerSearch) SearchPage(_ context.Context, q market.Query) (market.Page, error) {
	if q.Keyword != "" {
		return market.Page{Subcategories: f.keyword[q.Keyword]}, nil
	}
	if q.Postcode != "" {
		return market.Page{Total: 6, Listings: []market.Listing{
			{Title: "Gesloten aanhangwagen 750 kg", PriceType: "FIXED", PriceCents: 120000, CategoryID: 49},
			{Title: "Kipper Hulco 2700 kg", PriceType: "FIXED", PriceCents: 350000, CategoryID: 49},
		}}, nil
	}
	return market.Page{Total: 3717}, nil
}

func trailerSite() *trailerSearch {
	cat := func(id, parent, count int, name string) market.CategoryCount {
		return market.CategoryCount{Category: market.Category{ID: id, ParentID: parent, Name: name}, Count: count}
	}
	return &trailerSearch{keyword: map[string][]market.CategoryCount{
		"aanhangwagen": {cat(49, 48, 1678, "Aanhangers en Bagagewagens"), cat(1134, 48, 1082, "Aanhangwagen-onderdelen")},
		"aanhanger":    {cat(49, 48, 900, "Aanhangers en Bagagewagens")},
		"trailer":      {cat(49, 48, 322, "Aanhangers en Bagagewagens"), cat(414, 395, 400, "Paarden en Pony's | Trailers en Aanhangwagens")},
	}}
}

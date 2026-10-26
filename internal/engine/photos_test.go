package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

func TestPhotosAreDescribedBeforeJudging(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	judgeQuote := "red and black clips"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		content := `{"description":"Two wooden speakers. On the back: red and black clips, a bass port, a bass port, a bass port."}`
		if body["model"] != "vision" {
			content = `{"requirements":[{"number":1,"basis":"photo","quote":"` + judgeQuote + `","reason":"clamp terminals","status":"met"}]}`
		}
		resp, _ := json.Marshal(map[string]any{"message": map[string]string{"content": content}})
		rw.Write(resp)
	}))
	defer srv.Close()
	llm.HostOverride = srv.URL
	defer func() { llm.HostOverride = "" }()

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := &Engine{Config: config.Config{Model: "text"}, LLM: &llm.Manager{}, Store: st, vision: "vision"}
	seen, err := e.describePhotos(context.Background(), [][]byte{[]byte("jpeg")})
	if err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if seen != "Two wooden speakers. On the back: red and black clips, a bass port" {
		t.Errorf("description %q; repeated phrases should be dropped", seen)
	}
	w := watch.Watch{Mode: watch.ModeBuy, MustHave: []string{"rood-zwarte draad"},
		Checks: []watch.Check{{Wish: "rood-zwarte draad", Kind: "passieve luidsprekers", Means: "heeft klemmen voor luidsprekerkabel", Photo: "red and black terminals"}}}
	p := prepared{listing: market.Listing{Title: "Luidsprekers"}, photos: 1, seen: seen}
	ev, err := e.evaluateBuy(context.Background(), w, "Title: Luidsprekers", p)
	if err != nil {
		t.Fatal(err)
	}
	if r := ev.Requirements[0]; r.Status != "met" || r.Basis != "photo" || ev.PhotosSeen != 1 || ev.Seen != seen {
		t.Errorf("photo verdict quoting the description: %+v", ev)
	}

	describe, judge := requests[0], requests[1]
	if b, _ := json.Marshal(describe); strings.Contains(string(b), "red and black terminals") || strings.Contains(string(b), "passieve") || !strings.Contains(string(b), "images") {
		t.Errorf("the photo model must see the photos but not what to look for: %s", b)
	}
	if b, _ := json.Marshal(judge); strings.Contains(string(b), "images") || !strings.Contains(string(b), "red and black clips") {
		t.Errorf("the judge must read the description, not the photos: %s", b)
	}

	judgeQuote = "red and black terminals"
	ev, _ = e.evaluateBuy(context.Background(), w, "Title: Luidsprekers", p)
	if ev.Requirements[0].Status != "unknown" {
		t.Errorf("photo verdict on something the description does not say: %+v", ev.Requirements[0])
	}
}

package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/websearch"
)

const ddgPage = `<div class="result results_links web-result ">
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fiq3">KEF iQ3 Specifications</a>
<a class="result__snippet" href="x">Type: 2 way. Recommended Amplifier: 15 to 120W. Impedance 8 ohm.</a></div>
<div class="result results_links web-result ">
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fls50">KEF LS50 Wireless</a>
<a class="result__snippet" href="x">Active speaker with built-in amplifiers.</a></div>`

func TestLookupUsesTheAdsModel(t *testing.T) {
	var model atomic.Value
	model.Store(`{"brand":"Kef","model":"iq3"}`)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]any{"message": map[string]string{"content": model.Load().(string)}})
		rw.Write(resp)
	}))
	defer llmSrv.Close()
	llm.HostOverride = llmSrv.URL
	defer func() { llm.HostOverride = "" }()

	var searches atomic.Int32
	var query atomic.Value
	ddg := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		query.Store(r.URL.Query().Get("q"))
		rw.Write([]byte(ddgPage))
	}))
	defer ddg.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := &Engine{Config: config.Config{Model: "text"}, LLM: &llm.Manager{}, Store: st,
		Web: &websearch.Client{BaseURL: ddg.URL, Gap: time.Millisecond}, webOn: true}
	l := market.Listing{Title: "Prachtige luidsprekers. Kef iq3", Description: "Weinig gebruikt."}

	got, found := e.lookup(context.Background(), l)
	if got != "Kef iq3" || query.Load() != "Kef iq3 specifications" {
		t.Errorf("looked up %q with %q", got, query.Load())
	}
	if !strings.Contains(found, "Recommended Amplifier: 15 to 120W") || strings.Contains(found, "LS50") {
		t.Errorf("results for the judge should be about this model only: %q", found)
	}
	if _, again := e.lookup(context.Background(), l); again != found || searches.Load() != 1 {
		t.Errorf("a model should be searched once: %d searches", searches.Load())
	}

	// A model the ad does not name is a guess, and is not looked up.
	model.Store(`{"brand":"KEF","model":"LS50"}`)
	if got, _ := e.lookup(context.Background(), l); got != "" {
		t.Errorf("looked up %q, which the ad does not name", got)
	}
	model.Store(`{"brand":"Kef","model":""}`)
	if got, _ := e.lookup(context.Background(), market.Listing{Title: "Kef luidsprekers"}); got != "" {
		t.Errorf("looked up %q without a model", got)
	}
}

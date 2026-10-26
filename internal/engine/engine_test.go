package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/telegram"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

func listingJSON(id, title, desc string, cents int) string {
	return fmt.Sprintf(`{"itemId":%q,"title":%q,"description":%q,"priceInfo":{"priceCents":%d,"priceType":"FIXED"},
		"location":{"cityName":"Gent","distanceMeters":20000},"categoryId":48,"vipUrl":"/v/x/%s","attributes":[]}`,
		id, title, desc, cents, id)
}

type harness struct {
	t      *testing.T
	engine *Engine
	store  *store.Store

	mu       sync.Mutex
	searches int
	// results returns the listings for the n-th search request (starting at 1).
	results func(n int) []string
	llmBody string
	sent    []string
}

func newHarness(t *testing.T, w watch.Watch, sites []string) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	if err := watch.Save(w); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t}

	detail := `<html><div data-collapsable="description">geremd tot 750 kg, nette staat</div></html>`
	marketSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/lrp/api/search") {
			rw.Write([]byte(detail))
			return
		}
		h.mu.Lock()
		h.searches++
		n := h.searches
		h.mu.Unlock()
		rw.Write([]byte(`{"listings":[` + strings.Join(h.results(n), ",") + `]}`))
	}))
	t.Cleanup(marketSrv.Close)

	llmSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]any{"message": map[string]string{"content": h.llmBody}})
		rw.Write(resp)
	}))
	t.Cleanup(llmSrv.Close)

	tgSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.sent = append(h.sent, r.URL.Path+" "+string(b))
		h.mu.Unlock()
		rw.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(tgSrv.Close)

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st

	BaseURL = marketSrv.URL
	llm.HostOverride = llmSrv.URL
	t.Cleanup(func() { BaseURL = ""; llm.HostOverride = "" })

	h.engine = &Engine{
		Config: config.Config{Sites: sites, Model: "test", MinDealScore: 4, MinLearningScore: 7, DistanceM: 50000},
		LLM:    &llm.Manager{},
		Store:  st,
		Bot:    &telegram.Bot{Token: "t", API: tgSrv.URL},
		ChatID: 42,
		Delay:  time.Millisecond,
	}
	return h
}

func (h *harness) scan() ScanResult {
	h.t.Helper()
	res, err := h.engine.Scan(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func trailerWatch() watch.Watch {
	return watch.Watch{Name: "trailer", Enabled: true, Mode: watch.ModeBuy,
		Queries: []string{"aanhangwagen"}, Category: "Auto diversen", MaxPrice: 900,
		MustHave: []string{"braked"}, MinScore: 4}
}

func TestBrowseSubcategories(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		got = append(got, fmt.Sprintf("l1=%s l2=%v q=%s limit=%s", q.Get("l1CategoryId"), q["l2CategoryIds"], q.Get("query"), q.Get("limit")))
		mu.Unlock()
		rw.Write([]byte(`{"listings":[]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL
	defer func() { BaseURL = "" }()

	e := &Engine{Config: config.Config{Postcode: "9000", DistanceM: 50000}, Delay: time.Millisecond}
	w := watch.Watch{Category: "Auto diversen", Subcategories: []int{49}, BrowseSubcategories: true}
	if _, err := e.search(context.Background(), e.client(market.TwoDeHands), w); err != nil {
		t.Fatal(err)
	}
	w = watch.Watch{Category: "Auto diversen", Subcategories: []int{49}, OnlySubcategories: true, Queries: []string{"bagagewagen"}}
	if _, err := e.search(context.Background(), e.client(market.TwoDeHands), w); err != nil {
		t.Fatal(err)
	}
	want := []string{"l1=48 l2=[49] q= limit=100", "l1=48 l2=[49] q=bagagewagen limit=30"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s", strings.Join(got, "\n"))
	}
}

func TestExistingListingsAreJudged(t *testing.T) {
	old := listingJSON("m1", "Anssems geremd", "geremd tot 750 kg", 65000)
	h := newHarness(t, trailerWatch(), []string{"2dehands"})
	h.llmBody = `{"requirements":[{"number":1,"status":"met","quote":"geremd tot 750 kg"}]}`
	h.results = func(n int) []string {
		if n == 1 {
			return []string{old}
		}
		return []string{old,
			listingJSON("m3", "Hapert geremd", "geremd tot 750 kg", 60000),
			listingJSON("m4", "Aanhangwagen gezocht", "ik koop", 0)}
	}

	if res := h.scan(); res.Evaluated != 1 || res.Alerts != 1 {
		t.Fatalf("first scan: %+v; a listing already online should be judged too", res)
	}
	res := h.scan()
	if res.Evaluated != 1 || res.Alerts != 1 {
		t.Fatalf("second scan: %+v; only the new listing should be judged", res)
	}
	if len(h.sent) != 2 || !strings.Contains(h.sent[0], "Anssems") || !strings.Contains(h.sent[1], "Hapert") {
		t.Fatalf("sent: %v", h.sent)
	}
	if row, _ := h.store.Get("trailer", "m1"); row == nil || row.Status != store.StatusEvaluated || !row.Notified {
		t.Fatalf("existing listing: %+v", row)
	}
	if row, _ := h.store.Get("trailer", "m4"); row == nil || row.Status != store.StatusSkipped {
		t.Fatalf("wanted ad not skipped: %+v", row)
	}
	if res := h.scan(); res.Evaluated != 0 || res.Alerts != 0 {
		t.Fatalf("third scan repeated work: %+v", res)
	}
}

func TestBacklogIsJudgedInBatchesAndAlertsTheBest(t *testing.T) {
	h := newHarness(t, trailerWatch(), []string{"2dehands"})
	h.llmBody = `{"requirements":[{"number":1,"status":"met","quote":"geremd tot 750 kg"}]}`
	var listings []string
	for i := range backlogPerScan + 5 {
		// Cheaper listings score higher against the median.
		listings = append(listings, listingJSON(fmt.Sprintf("b%d", i), fmt.Sprintf("Aanhangwagen geremd %d", i), "geremd tot 750 kg", 40000+i*2000))
	}
	h.results = func(int) []string { return listings }

	res := h.scan()
	if res.Evaluated != backlogPerScan || res.Alerts != backlogAlerts {
		t.Fatalf("first scan: %+v", res)
	}
	for i, msg := range h.sent {
		if !strings.Contains(msg, fmt.Sprintf("Aanhangwagen geremd %d", i)) {
			t.Errorf("alert %d is not the next best: %s", i, msg[:min(len(msg), 120)])
		}
	}
	held, _ := h.store.Get("trailer", fmt.Sprintf("b%d", backlogAlerts))
	if held == nil || held.Notified || !strings.Contains(held.Reason, "already listed") {
		t.Fatalf("held back listing: %+v", held)
	}
	if res := h.scan(); res.Evaluated != 5 {
		t.Fatalf("second scan: %+v; the rest of the backlog", res)
	}
	if res := h.scan(); res.Evaluated != 0 {
		t.Fatalf("third scan: %+v", res)
	}
}

func TestEverySiteIsJudgedOnce(t *testing.T) {
	h := newHarness(t, trailerWatch(), []string{"2dehands", "marktplaats"})
	h.llmBody = `{"requirements":[{"number":1,"status":"met","quote":"geremd tot 750 kg"}]}`
	// Odd requests are 2dehands, even ones Marktplaats; each has its own listing.
	h.results = func(n int) []string {
		if n%2 == 1 {
			return []string{listingJSON("m1", "Anssems geremd", "geremd tot 750 kg", 65000)}
		}
		return []string{listingJSON("m2", "Humbaur geremd", "geremd tot 750 kg", 70000)}
	}
	if res := h.scan(); res.Evaluated != 2 {
		t.Fatalf("first scan: %+v; both sites' listings should be judged", res)
	}
	if res := h.scan(); res.Evaluated != 0 || res.Alerts != 0 {
		t.Fatalf("listings were judged twice: %+v", res)
	}
}

func TestRephrasedRequirementStillCounts(t *testing.T) {
	h := newHarness(t, trailerWatch(), []string{"2dehands"})
	// The model renames nothing (it only returns numbers) but says the hard
	// requirement is unmet, so the listing must not alert.
	h.llmBody = `{"requirements":[{"number":1,"status":"unmet","quote":"ongeremd"}]}`
	old := listingJSON("m1", "Anssems geremd", "geremd tot 750 kg", 65000)
	h.results = func(n int) []string {
		if n == 1 {
			return []string{old}
		}
		return []string{old, listingJSON("m5", "Aanhangwagen ongeremd", "ongeremd, 500 kg", 40000)}
	}
	h.scan()
	if res := h.scan(); res.Evaluated != 1 || res.Alerts != 0 {
		t.Fatalf("unmet hard requirement alerted: %+v", res)
	}
	row, _ := h.store.Get("trailer", "m5")
	if row == nil || !strings.Contains(row.EvalJSON, `"name":"braked"`) {
		t.Fatalf("requirement name not taken from the watch: %+v", row)
	}
	if !strings.Contains(row.Reason, "Not alerted") {
		t.Errorf("decision %q", row.Reason)
	}
}

func TestFeedbackButton(t *testing.T) {
	h := newHarness(t, trailerWatch(), []string{"2dehands"})
	var u telegram.Update
	raw := `{"update_id":1,"callback_query":{"id":"cb1","data":"fb:n:trailer:m9","message":{"chat":{"id":42}}}}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	h.engine.handle(u)
	got, err := h.store.FeedbackFor("trailer", 5)
	if err != nil || len(got) != 1 || got[0].Verdict != store.NotRelevant {
		t.Fatalf("feedback %+v %v", got, err)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "answerCallbackQuery") {
		t.Fatalf("button press not acknowledged: %v", h.sent)
	}
}

func TestCommands(t *testing.T) {
	h := newHarness(t, trailerWatch(), []string{"2dehands"})
	if reply := h.engine.command("/pause trailer"); !strings.Contains(reply, "gepauzeerd") {
		t.Fatalf("pause: %s", reply)
	}
	if w, _ := watch.Load("trailer"); w.Enabled {
		t.Fatal("watch still enabled")
	}
	if reply := h.engine.command("/list"); !strings.Contains(reply, "trailer (gepauzeerd)") {
		t.Fatalf("list: %s", reply)
	}
	if reply := h.engine.command("/start abc"); !strings.Contains(reply, "/pause") {
		t.Fatalf("help: %s", reply)
	}
}

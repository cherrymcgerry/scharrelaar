package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

func TestStatusWatchesAndInterview(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := &Server{Version: "test"}
	s.interview = func(ctx context.Context, description string, a flow.Asker) (watch.Draft, error) {
		a.Progress("Reading your request...")
		got, err := a.Choose(flow.Choice{
			Title: "Where is it listed?", Multiple: true, Min: 1,
			Options: []flow.Option{{Label: "Trailers (10 listings)", Value: "49", Selected: true}},
		})
		if err != nil {
			return watch.Draft{}, err
		}
		if len(got) != 1 || got[0] != "49" {
			return watch.Draft{}, fmt.Errorf("answer %v", got)
		}
		return watch.Draft{Watch: watch.Watch{
			Name: "aanhangwagen", Enabled: true, Mode: watch.ModeBuy,
			Queries: []string{"aanhangwagen"}, Description: description,
		}}, nil
	}
	h := s.Handler()

	status := getJSON(t, h, "/api/status")
	if status["app"] != "scharrelaar" || status["configured"] != false {
		t.Fatalf("status %+v", status)
	}
	if strings.Contains(mustString(status), "123456:ABC") {
		t.Fatal("status leaked a token")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/status", nil)
	req.Host = "evil.example"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host %d", rec.Code)
	}

	rec = postRaw(h, "/api/sessions", `{"kind":"interview","description":"een aanhangwagen"}`, "http://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin %d %s", rec.Code, rec.Body)
	}

	rec = postRaw(h, "/api/sessions", `{"kind":"interview","description":"een aanhangwagen"}`, "http://127.0.0.1:8765")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	var view sessionView
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := getBody(t, h, "/api/sessions/"+created.ID)
		json.Unmarshal(body, &view)
		if view.Question != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if view.Question == nil {
		t.Fatalf("no question: %+v", view)
	}
	if !strings.Contains(strings.Join(view.Log, "\n"), "Reading your request") {
		t.Errorf("log %v", view.Log)
	}

	answer := fmt.Sprintf(`{"question_id":%q,"values":["49"]}`, view.Question.ID)
	rec = postRaw(h, "/api/sessions/"+created.ID+"/answer", answer, "http://127.0.0.1:8765")
	if rec.Code != http.StatusOK {
		t.Fatalf("answer %d %s", rec.Code, rec.Body)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := getBody(t, h, "/api/sessions/"+created.ID)
		json.Unmarshal(body, &view)
		if view.Done {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !view.Done || view.Result == nil || view.Result.Name != "aanhangwagen" {
		t.Fatalf("result %+v err %q", view.Result, view.Error)
	}

	rec = postRaw(h, "/api/sessions/"+created.ID+"/save", `{"name":"aanhangwagen"}`, "http://127.0.0.1:8765")
	if rec.Code != http.StatusOK {
		t.Fatalf("save %d %s", rec.Code, rec.Body)
	}

	list := getBody(t, h, "/api/watches")
	if !strings.Contains(string(list), `"aanhangwagen"`) {
		t.Fatalf("watches %s", list)
	}

	rec = postRaw(h, "/api/watches/aanhangwagen/pause", `{}`, "http://127.0.0.1:8765")
	if rec.Code != http.StatusOK {
		t.Fatalf("pause %d %s", rec.Code, rec.Body)
	}
	list = getBody(t, h, "/api/watches")
	if !strings.Contains(string(list), `"enabled":false`) {
		t.Fatalf("not paused: %s", list)
	}

	rec = postRaw(h, "/api/watches/aanhangwagen/resume", `{}`, "http://127.0.0.1:8765")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	req = httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8765/api/watches/aanhangwagen", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8765")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete %d %s", rec.Code, rec.Body)
	}
	list = getBody(t, h, "/api/watches")
	if strings.Contains(string(list), "aanhangwagen") {
		t.Fatalf("still listed: %s", list)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Scharrelaar") {
		t.Fatalf("page %d %s", rec.Code, rec.Body.String()[:min(200, rec.Body.Len())])
	}
}

func TestListingsEndpoint(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "listings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	raw := `{"requirements":[{"name":"braked","status":"met","quote":"geremd"}],"notes":["hard requirement unmet"]}`
	if err := st.Record(store.Row{
		Watch: "trailer", ItemID: "m1", Title: "Anssems", PriceCents: 65000, PriceType: "FIXED",
		URL: "https://www.2dehands.be/v/m1", Site: "2dehands", Status: store.StatusEvaluated,
		Reason: "Not alerted. Deal score 1 is below 4.", Score: 1, EvalJSON: raw,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Record(store.Row{
		Watch: "trailer", ItemID: "m2", Title: "Old", PriceCents: 100, PriceType: "FIXED", URL: "https://www.2dehands.be/v/m2",
		Status: store.StatusSkipped, Reason: "price above max",
	}); err != nil {
		t.Fatal(err)
	}

	h := (&Server{Store: st}).Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/watches/trailer/listings", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listings %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Total    int `json:"total"`
		Judged   int `json:"judged"`
		Skipped  int `json:"skipped"`
		Listings []struct {
			Title    string `json:"title"`
			Decision string `json:"decision"`
			Findings []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Quote  string `json:"quote"`
			} `json:"findings"`
			Notes []string `json:"notes"`
		} `json:"listings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 2 || body.Judged != 1 || body.Skipped != 1 || len(body.Listings) != 2 {
		t.Fatalf("body %+v", body)
	}
	if body.Listings[0].Decision != "Not alerted. Deal score 1 is below 4." || len(body.Listings[0].Findings) != 1 || body.Listings[0].Findings[0].Quote != "geremd" {
		t.Fatalf("judged listing %+v", body.Listings[0])
	}
	if body.Listings[1].Decision != "Not alerted: price above max" {
		t.Fatalf("skipped decision %q", body.Listings[1].Decision)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "http://evil.example/api/watches/trailer/listings", nil)
	req.Host = "evil.example"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host %d", rec.Code)
	}
}

func getJSON(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	var out map[string]any
	json.Unmarshal(getBody(t, h, path), &out)
	return out
}

func getBody(t *testing.T, h http.Handler, path string) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765"+path, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

func postRaw(h http.Handler, path, body, origin string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765"+path, strings.NewReader(body))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func mustString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

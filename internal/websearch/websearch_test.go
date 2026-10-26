package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSearchParsesResults(t *testing.T) {
	page, err := os.ReadFile("testdata/ddg.html")
	if err != nil {
		t.Fatal(err)
	}
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		rw.Write(page)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Gap: time.Millisecond}
	got, err := c.Search(context.Background(), "Pioneer TAD S-3EX specifications", 3)
	if err != nil {
		t.Fatal(err)
	}
	if query != "Pioneer TAD S-3EX specifications" {
		t.Errorf("query %q", query)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(got), got)
	}
	first := got[0]
	if first.URL != "https://audio-database.com/PIONEER-EXCLUSIVE/speaker/s-3ex.html" ||
		!strings.Contains(first.Title, "S-3EX") || !strings.Contains(first.Snippet, "inherits TAD technology") {
		t.Errorf("first result %+v", first)
	}
}

func TestSearchReportsBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte(`<html><div class="anomaly-modal__title">Unfortunately, bots use DuckDuckGo too.</div></html>`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Gap: time.Millisecond}
	if _, err := c.Search(context.Background(), "x", 3); !errors.Is(err, ErrBlocked) {
		t.Errorf("err %v, want ErrBlocked", err)
	}
}

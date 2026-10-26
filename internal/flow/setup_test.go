package flow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
)

func TestSetupQuestions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	prev := saveToken
	var gotToken string
	saveToken = func(tok string) error {
		gotToken = tok
		return nil
	}
	t.Cleanup(func() { saveToken = prev })

	ask := &script{inputs: map[string]string{
		"Your postcode":       "9000",
		"Search radius in km": "30",
		"Paste the bot token": "123456:ABC-def",
	}}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"username":"scharrelbot","first_name":"Scharrelaar"}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			fmt.Fprint(w, `{"ok":true,"result":{}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			ask.mu.Lock()
			link := ask.link
			ask.mu.Unlock()
			code := ""
			if i := strings.LastIndex(link, "start="); i >= 0 {
				code = link[i+len("start="):]
			}
			fmt.Fprintf(w, `{"ok":true,"result":[{"update_id":1,"message":{"text":"/start %s","chat":{"id":42}}}]}`, code)
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := config.Default()
	err := setup(ctx, &cfg, &llm.Manager{}, ask, setupOpts{skipModel: true, telegramAPI: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != llm.Choices()[0].Name {
		t.Errorf("model %q", cfg.Model)
	}
	if cfg.Country != "BE" || cfg.Postcode != "9000" || cfg.DistanceM != 30000 {
		t.Errorf("location %+v", cfg)
	}
	if len(cfg.Sites) != 1 || cfg.Sites[0] != "2dehands" || cfg.ChatID != "42" {
		t.Errorf("sites %v chat %q", cfg.Sites, cfg.ChatID)
	}
	if gotToken != "123456:ABC-def" {
		t.Errorf("token %q", gotToken)
	}
	if !ask.asked("Paired.") || !ask.asked("Token accepted: @scharrelbot") {
		t.Errorf("progress %v", ask.asks)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Postcode != "9000" || saved.ChatID != "42" || saved.Model != cfg.Model {
		t.Errorf("saved %+v", saved)
	}
}

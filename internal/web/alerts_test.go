package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

type fakeEngine struct {
	on bool
}

func (f *fakeEngine) Pause()  {}
func (f *fakeEngine) Resume() {}
func (f *fakeEngine) Start() AlertState {
	f.on = true
	return f.State()
}
func (f *fakeEngine) Stop() AlertState {
	f.on = false
	return f.State()
}
func (f *fakeEngine) State() AlertState {
	if f.on {
		return AlertState{State: AlertsRunning}
	}
	return AlertState{State: AlertsStopped}
}

func TestStartStopAndQuit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	engine := &fakeEngine{on: true}
	quit := make(chan struct{})
	h := (&Server{Engine: engine, Quit: func() { close(quit) }}).Handler()

	alerts := func() string {
		status := getJSON(t, h, "/api/status")
		a, _ := status["alerts"].(map[string]any)
		s, _ := a["state"].(string)
		return s
	}
	if got := alerts(); got != AlertsRunning {
		t.Fatalf("alerts %q", got)
	}

	if rec := postRaw(h, "/api/alerts/stop", `{}`, "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin stopped alerts: %d", rec.Code)
	}
	if !engine.on {
		t.Fatal("alerts stopped by a foreign page")
	}

	rec := postRaw(h, "/api/alerts/stop", `{}`, "http://127.0.0.1:8765")
	var state AlertState
	json.Unmarshal(rec.Body.Bytes(), &state)
	if rec.Code != http.StatusOK || state.State != AlertsStopped || alerts() != AlertsStopped {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body)
	}
	if rec := postRaw(h, "/api/alerts/start", `{}`, "http://127.0.0.1:8765"); rec.Code != http.StatusOK || alerts() != AlertsRunning {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}

	if rec := postRaw(h, "/api/quit", `{}`, "http://127.0.0.1:8765"); rec.Code != http.StatusOK {
		t.Fatalf("quit: %d %s", rec.Code, rec.Body)
	}
	<-quit
}

func TestWebLookupSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	h := (&Server{}).Handler()
	lookup := func() bool {
		on, _ := getJSON(t, h, "/api/status")["web_lookup"].(bool)
		return on
	}
	if lookup() {
		t.Fatal("web lookup must be off by default")
	}
	if rec := postRaw(h, "/api/web-lookup/on", `{}`, "http://evil.example"); rec.Code != http.StatusForbidden || lookup() {
		t.Fatalf("a foreign page turned web lookup on: %d", rec.Code)
	}
	if rec := postRaw(h, "/api/web-lookup/on", `{}`, "http://127.0.0.1:8765"); rec.Code != http.StatusOK || !lookup() {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	if rec := postRaw(h, "/api/web-lookup/off", `{}`, "http://127.0.0.1:8765"); rec.Code != http.StatusOK || lookup() {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
}

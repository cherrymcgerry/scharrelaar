// Package web serves the localhost interface: setup, the search interview, and
// managing searches. It listens on 127.0.0.1 only.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/cli"
	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

//go:embed static/*
var staticFS embed.FS

// EngineSwitch starts and stops the alerts. Setup also pauses scanning while it
// pairs Telegram: pairing and the scanner both read Telegram updates, so only
// one may run.
type EngineSwitch interface {
	Pause()
	Resume()
	Start() AlertState
	Stop() AlertState
	State() AlertState
}

// Alert states.
const (
	AlertsRunning      = "running"
	AlertsStopped      = "stopped"
	AlertsPaused       = "paused for setup"
	AlertsUnconfigured = "not configured"
	AlertsError        = "error"
)

// AlertState is whether alerts run, and what went wrong if they should but do not.
type AlertState struct {
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
}

// Server is the localhost interface.
type Server struct {
	Port    int
	Version string
	LLM     *llm.Manager
	Store   *store.Store
	Engine  EngineSwitch
	// Quit ends the process. It is used to uninstall, which cannot remove files
	// the running process holds open.
	Quit func()

	// interview and setup replace the real flows in tests.
	interview func(ctx context.Context, description string, a flow.Asker) (watch.Draft, error)
	setup     func(ctx context.Context, a flow.Asker) error

	baseCtx context.Context

	mu        sync.Mutex
	sessions  map[string]*Session
	setupBusy bool
}

// Handler is the HTTP handler, including the loopback checks.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/watches", s.handleWatches)
	mux.HandleFunc("GET /api/watches/{name}/listings", s.handleListings)
	mux.HandleFunc("POST /api/watches/{name}/pause", s.handlePause(false))
	mux.HandleFunc("POST /api/watches/{name}/resume", s.handlePause(true))
	mux.HandleFunc("DELETE /api/watches/{name}", s.handleDelete)
	mux.HandleFunc("POST /api/templates/{name}", s.handleTemplate)
	mux.HandleFunc("POST /api/alerts/start", s.handleAlerts(true))
	mux.HandleFunc("POST /api/alerts/stop", s.handleAlerts(false))
	mux.HandleFunc("POST /api/quit", s.handleQuit)
	mux.HandleFunc("POST /api/web-lookup/on", s.handleWebLookup(true))
	mux.HandleFunc("POST /api/web-lookup/off", s.handleWebLookup(false))
	mux.HandleFunc("POST /api/sessions", s.handleCreate)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGet)
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.handleAnswer)
	mux.HandleFunc("POST /api/sessions/{id}/save", s.handleSave)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleCancel)
	mux.Handle("GET /", staticHandler())
	return s.guard(mux)
}

// Listen claims the interface's port on 127.0.0.1. 0 means the default port.
func Listen(port int) (net.Listener, error) {
	if port == 0 {
		port = config.DefaultUIPort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("the interface could not use port %d (scharrelaar may already be running): %w", port, err)
	}
	return ln, nil
}

// Start listens on 127.0.0.1 and shuts down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	ln, err := Listen(s.Port)
	if err != nil {
		return err
	}
	s.Serve(ctx, ln)
	return nil
}

// Serve answers on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) {
	s.baseCtx = ctx
	srv := &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	go srv.Serve(ln)
}

func (s *Server) parent() context.Context {
	if s.baseCtx != nil {
		return s.baseCtx
	}
	return context.Background()
}

// guard rejects requests that did not come from this machine's own browser.
// The Host check blocks a public page from reaching the port by DNS rebinding,
// and the Origin check blocks another local page from changing anything.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !loopbackHost(r.Host) {
			writeErr(w, http.StatusForbidden, "this page is only available on this computer")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r.Header.Get("Origin"), r.Host) {
				writeErr(w, http.StatusForbidden, "this page is only available on this computer")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return host == "127.0.0.1" || host == "localhost"
}

func sameOrigin(origin, hostport string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	rest, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		return false
	}
	// Drop a path, if one was sent.
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest == hostport
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	telegram := "not paired"
	if cfg.ChatID != "" {
		telegram = "paired"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":        "scharrelaar",
		"version":    s.Version,
		"configured": cfg.Model != "" && cfg.ChatID != "",
		"model":      cfg.Model,
		"country":    cfg.Country,
		"postcode":   cfg.Postcode,
		"radius_km":  cfg.DistanceM / 1000,
		"sites":      cfg.Sites,
		"telegram":   telegram,
		"port":       cfg.UIPort,
		"service":    cli.ServiceStatus(),
		"alerts":     s.alertState(),
		"web_lookup": cfg.WebLookup,
	})
}

// handleWebLookup switches looking up brands and models on the web. The
// scanner reads the setting at the start of every scan.
func (s *Server) handleWebLookup(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := config.SetWebLookup(on); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"web_lookup": on})
	}
}

func (s *Server) alertState() AlertState {
	if s.Engine == nil {
		return AlertState{State: AlertsError, Problem: "scanning is not part of this process"}
	}
	return s.Engine.State()
}

func (s *Server) handleWatches(w http.ResponseWriter, r *http.Request) {
	watches, err := watch.All()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(watches))
	for _, item := range watches {
		seen := 0
		if s.Store != nil {
			if n, err := s.Store.CountForWatch(item.Name); err == nil {
				seen = n
			}
		}
		out = append(out, map[string]any{
			"name":    item.Name,
			"enabled": item.Enabled,
			"mode":    item.Mode,
			"detail":  watchDetail(item),
			"seen":    seen,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func watchDetail(w watch.Watch) string {
	switch {
	case w.BrowseSubcategories && w.Category != "":
		return "every listing in " + w.Category
	case len(w.Queries) > 0:
		return strings.Join(w.Queries, ", ")
	default:
		return w.Mode
	}
}

func (s *Server) handlePause(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := watch.SetEnabled(r.PathValue("name"), enabled); err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := watch.Delete(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTemplate(w http.ResponseWriter, r *http.Request) {
	tpl, ok := watch.Template(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown repair search")
		return
	}
	if err := watch.Save(tpl); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": tpl.Name})
}

func (s *Server) handleAlerts(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Engine == nil {
			writeErr(w, http.StatusServiceUnavailable, "scanning is not part of this process")
			return
		}
		s.mu.Lock()
		busy := s.setupBusy
		s.mu.Unlock()
		if busy {
			writeErr(w, http.StatusConflict, "setup is running; finish it first")
			return
		}
		state := s.Engine.Stop
		if on {
			state = s.Engine.Start
		}
		writeJSON(w, http.StatusOK, state())
	}
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if s.Quit == nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot quit from here")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go s.Quit()
}

type createBody struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body createBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := body.Kind
	if kind == "" {
		kind = "interview"
	}
	description := strings.TrimSpace(body.Description)
	if kind == "interview" && description == "" {
		writeErr(w, http.StatusBadRequest, "describe what you want")
		return
	}
	if kind != "interview" && kind != "setup" {
		writeErr(w, http.StatusBadRequest, "unknown session")
		return
	}
	if len(description) > 2000 {
		writeErr(w, http.StatusBadRequest, "that description is too long")
		return
	}

	s.dropIdle()
	ctx, cancel := context.WithCancel(s.parent())
	sess := &Session{
		id: newID(), kind: kind, description: description,
		cancel: cancel, cancelCtx: ctx, touched: time.Now(),
	}
	s.mu.Lock()
	if kind == "setup" && s.setupBusy {
		s.mu.Unlock()
		cancel()
		writeErr(w, http.StatusConflict, "setup is already running")
		return
	}
	if kind == "setup" {
		s.setupBusy = true
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	s.sessions[sess.id] = sess
	s.mu.Unlock()

	go s.run(ctx, sess)
	writeJSON(w, http.StatusCreated, map[string]string{"id": sess.id})
}

func (s *Server) run(ctx context.Context, sess *Session) {
	defer func() {
		if sess.kind != "setup" {
			return
		}
		s.mu.Lock()
		s.setupBusy = false
		s.mu.Unlock()
		if s.Engine != nil {
			s.Engine.Resume()
		}
	}()
	if sess.kind == "setup" && s.Engine != nil {
		s.Engine.Pause()
	}
	ask := webAsker{s: sess}
	if sess.kind == "setup" {
		err := s.doSetup(ctx, ask)
		if err == nil {
			sess.progress("Setup complete.")
		}
		sess.finish(nil, nil, err)
		return
	}
	draft, err := s.doInterview(ctx, sess.description, ask)
	if err != nil {
		sess.finish(nil, nil, err)
		return
	}
	text, examples := "", []flow.Example{}
	if s.interview == nil {
		cfg, loadErr := config.Load()
		if loadErr == nil {
			text, examples = flow.Preview(ctx, cfg, draft)
		}
	}
	saved := draft
	sess.finish(&resultView{
		Summary: draft.Summary(), Name: draft.Watch.Name, Description: sess.description,
		Preview: text, Examples: examples,
	}, &saved, nil)
}

func (s *Server) doInterview(ctx context.Context, description string, a flow.Asker) (watch.Draft, error) {
	if s.interview != nil {
		return s.interview(ctx, description, a)
	}
	cfg, err := config.Load()
	if err != nil {
		return watch.Draft{}, err
	}
	if cfg.Model == "" {
		return watch.Draft{}, errors.New("no model configured yet; finish setup first")
	}
	if s.LLM == nil {
		return watch.Draft{}, errors.New("the model server is not running")
	}
	return flow.Interview(ctx, cfg, s.LLM, description, a)
}

func (s *Server) doSetup(ctx context.Context, a flow.Asker) error {
	if s.setup != nil {
		return s.setup(ctx, a)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if s.LLM == nil {
		return errors.New("the model server is not available")
	}
	return flow.Setup(ctx, &cfg, s.LLM, a)
}

func (s *Server) session(id string) (*Session, bool) {
	s.dropIdle()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *Server) dropIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, sess := range s.sessions {
		sess.mu.Lock()
		idle := now.Sub(sess.touched) > sessionIdle
		sess.mu.Unlock()
		if idle {
			sess.cancel()
			delete(s.sessions, id)
		}
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "that session has ended")
		return
	}
	writeJSON(w, http.StatusOK, sess.view())
}

type answerBody struct {
	QuestionID string   `json:"question_id"`
	Values     []string `json:"values"`
	Text       string   `json:"text"`
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "that session has ended")
		return
	}
	var body answerBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := sess.deliver(body.QuestionID, answer{Values: body.Values, Text: body.Text}); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type saveBody struct {
	Name string `json:"name"`
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "that session has ended")
		return
	}
	var body saveBody
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	draft, ok := sess.takeDraft()
	if !ok {
		writeErr(w, http.StatusConflict, "this search is not ready to save")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = draft.Watch.Name
	}
	draft.Watch.Name = name
	if err := watch.Save(draft.Watch); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": draft.Watch.Name})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	sess.cancel()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decode(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("could not read the request")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

package main

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/cli"
	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/engine"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/telegram"
	"github.com/cherrymcgerry/scharrelaar/internal/web"
)

// supervisor starts and stops scanning. The user starts and stops alerts from
// the page; setup pauses scanning too, because pairing and the scanner both read
// Telegram updates.
type supervisor struct {
	parent context.Context
	llm    *llm.Manager
	store  *store.Store
	// autostart registers start at login whenever scanning starts. It is set for
	// the background process, not for a scharrel run in a terminal.
	autostart bool

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	paused  bool
	lastErr string
}

func newSupervisor(parent context.Context, mgr *llm.Manager, st *store.Store) *supervisor {
	return &supervisor{parent: parent, llm: mgr, store: st}
}

// Pause stops scanning until Resume. It implements web.EngineSwitch.
func (s *supervisor) Pause() {
	s.mu.Lock()
	s.paused = true
	s.mu.Unlock()
	s.halt("scanning paused for setup")
}

// Resume starts scanning again when setup is finished.
func (s *supervisor) Resume() {
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	s.ensure()
}

// Start turns alerts on, now and after a restart.
func (s *supervisor) Start() web.AlertState {
	if err := config.SetAlerts(true); err != nil {
		return web.AlertState{State: web.AlertsError, Problem: err.Error()}
	}
	problem := ""
	if err := cli.EnableAutostart(); err != nil {
		log.Printf("start at login: %v", err)
		problem = "Alerts run now, but could not be set to start at login: " + err.Error()
	}
	s.ensure()
	st := s.State()
	if st.Problem == "" {
		st.Problem = problem
	}
	return st
}

// Stop turns alerts off until Start, including after a restart. The page stays
// available.
func (s *supervisor) Stop() web.AlertState {
	if err := config.SetAlerts(false); err != nil {
		return web.AlertState{State: web.AlertsError, Problem: err.Error()}
	}
	problem := ""
	if err := cli.DisableAutostart(); err != nil {
		log.Printf("start at login: %v", err)
		problem = err.Error()
	}
	s.halt("alerts stopped")
	st := s.State()
	st.Problem = problem
	return st
}

// State reports whether scanning runs, and why not.
func (s *supervisor) State() web.AlertState {
	s.mu.Lock()
	running, paused, lastErr := s.cancel != nil, s.paused, s.lastErr
	s.mu.Unlock()
	switch {
	case running:
		return web.AlertState{State: web.AlertsRunning}
	case paused:
		return web.AlertState{State: web.AlertsPaused}
	}
	cfg, err := config.Load()
	if err != nil {
		return web.AlertState{State: web.AlertsError, Problem: err.Error()}
	}
	if cfg.AlertsOff {
		return web.AlertState{State: web.AlertsStopped}
	}
	if !configured(cfg) {
		return web.AlertState{State: web.AlertsUnconfigured}
	}
	return web.AlertState{State: web.AlertsError, Problem: orDefault(lastErr, "scanning is not running")}
}

// halt stops scanning and waits until the scanner has released Telegram.
func (s *supervisor) halt(why string) {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel = nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	log.Print(why)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Print("scanning did not stop within 5s; continuing")
	}
}

// shutdown stops scanning for good when the process exits.
func (s *supervisor) shutdown() {
	s.mu.Lock()
	s.paused = true
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func configured(cfg config.Config) bool {
	token, err := config.Token()
	return err == nil && token != "" && cfg.Model != "" && cfg.ChatID != ""
}

// ensure starts scanning when the app is configured, alerts are on and setup is
// not running.
func (s *supervisor) ensure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused || s.cancel != nil || s.parent.Err() != nil {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v", err)
		s.lastErr = err.Error()
		return
	}
	if cfg.AlertsOff {
		log.Print("alerts are stopped; start them from the page")
		return
	}
	token, _ := config.Token()
	if !configured(cfg) {
		log.Print("not configured yet; open the page to finish setup")
		return
	}
	chatID, err := strconv.ParseInt(cfg.ChatID, 10, 64)
	if err != nil {
		log.Printf("invalid chat id %q", cfg.ChatID)
		s.lastErr = "invalid Telegram chat id; run setup again"
		return
	}
	if s.autostart {
		if err := cli.EnableAutostart(); err != nil {
			log.Printf("start at login: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(s.parent)
	s.cancel = cancel
	s.lastErr = ""
	done := make(chan struct{})
	s.done = done
	e := &engine.Engine{
		Config: cfg, LLM: s.llm, Store: s.store,
		Bot: &telegram.Bot{Token: token}, ChatID: chatID,
		// Thinking models spend most of NumPredict thinking before they answer.
		Options: llm.Options{NumCtx: 4096, NumPredict: 3000},
	}
	log.Printf("scanning: model %s, sites %s", cfg.Model, strings.Join(cfg.Sites, ", "))
	go func() {
		defer close(done)
		err := e.Run(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		log.Printf("scanning stopped: %v", err)
		s.mu.Lock()
		if s.done == done && s.cancel != nil {
			s.cancel()
			s.cancel = nil
			s.lastErr = err.Error()
		}
		s.mu.Unlock()
	}()
}

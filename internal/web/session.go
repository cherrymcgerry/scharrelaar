package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/flow"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
	"github.com/skip2/go-qrcode"
)

const sessionIdle = 30 * time.Minute

var errStale = errors.New("that question was already answered")

// answer is what the browser sent for the open question.
type answer struct {
	Values []string
	Text   string
}

type optionView struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Selected bool   `json:"selected,omitempty"`
}

type questionView struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Multiple    bool         `json:"multiple,omitempty"`
	Min         int          `json:"min,omitempty"`
	Secret      bool         `json:"secret,omitempty"`
	Default     string       `json:"default,omitempty"`
	Error       string       `json:"error,omitempty"`
	Options     []optionView `json:"options,omitempty"`
}

type cardView struct {
	Title string   `json:"title,omitempty"`
	Lines []string `json:"lines,omitempty"`
	Link  string   `json:"link,omitempty"`
	QRPNG string   `json:"qr_png,omitempty"`
}

type resultView struct {
	Summary     string         `json:"summary"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Preview     string         `json:"preview,omitempty"`
	Examples    []flow.Example `json:"examples,omitempty"`
}

type sessionView struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	Log      []string      `json:"log"`
	Card     *cardView     `json:"card,omitempty"`
	Question *questionView `json:"question,omitempty"`
	Done     bool          `json:"done"`
	Error    string        `json:"error,omitempty"`
	Result   *resultView   `json:"result,omitempty"`
}

// Session is one setup or interview running for the browser.
type Session struct {
	id          string
	kind        string
	description string
	cancel      context.CancelFunc
	cancelCtx   context.Context

	mu       sync.Mutex
	log      []string
	card     *cardView
	question *questionView
	reply    chan answer
	done     bool
	errMsg   string
	result   *resultView
	draft    *watch.Draft
	touched  time.Time
}

func (s *Session) progress(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, msg)
	if len(s.log) > 400 {
		s.log = s.log[len(s.log)-400:]
	}
	s.touched = time.Now()
}

func (s *Session) setCard(c cardView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Lines = append([]string{}, c.Lines...)
	s.card = &c
	s.touched = time.Now()
}

// wait shows a question until the browser answers or the session is cancelled.
func (s *Session) wait(q questionView) (answer, error) {
	reply := make(chan answer, 1)
	s.mu.Lock()
	q.Options = append([]optionView{}, q.Options...)
	s.question = &q
	s.reply = reply
	s.touched = time.Now()
	s.mu.Unlock()

	select {
	case ans := <-reply:
		return ans, nil
	case <-s.cancelCtx.Done():
		return answer{}, context.Canceled
	}
}

func (s *Session) deliver(id string, ans answer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.question == nil || s.question.ID != id || s.reply == nil {
		return errStale
	}
	s.reply <- ans
	s.reply = nil
	// Hide the question while the answer is checked, so a long model call shows
	// progress instead of the form that was just sent.
	s.question = nil
	s.touched = time.Now()
	return nil
}

func (s *Session) finish(res *resultView, draft *watch.Draft, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.question = nil
	s.card = nil
	s.reply = nil
	s.result = res
	s.draft = draft
	s.touched = time.Now()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.errMsg = "Cancelled."
		} else {
			s.errMsg = err.Error()
		}
	}
}

func (s *Session) view() sessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := sessionView{
		ID: s.id, Kind: s.kind, Done: s.done, Error: s.errMsg,
		Log: append([]string{}, s.log...),
	}
	if s.card != nil {
		c := *s.card
		c.Lines = append([]string{}, s.card.Lines...)
		v.Card = &c
	}
	if s.question != nil {
		q := *s.question
		q.Options = append([]optionView{}, s.question.Options...)
		v.Question = &q
	}
	if s.result != nil {
		r := *s.result
		r.Examples = append([]flow.Example{}, s.result.Examples...)
		v.Result = &r
	}
	return v
}

func (s *Session) takeDraft() (watch.Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draft == nil {
		return watch.Draft{}, false
	}
	return *s.draft, true
}

// webAsker is the flow.Asker backed by a browser session.
type webAsker struct{ s *Session }

func (a webAsker) Progress(msg string) { a.s.progress(msg) }

func (a webAsker) Show(c flow.Card) {
	view := cardView{Title: c.Title, Lines: c.Lines, Link: c.Link}
	if c.QR != "" {
		if png, err := qrcode.Encode(c.QR, qrcode.Low, 256); err == nil {
			view.QRPNG = base64.StdEncoding.EncodeToString(png)
		}
	}
	a.s.setCard(view)
}

func (a webAsker) Choose(q flow.Choice) ([]string, error) {
	min := q.Min
	if !q.Multiple {
		min = 1
	}
	id := newID()
	msg := ""
	var opts []optionView
	for _, o := range q.Options {
		opts = append(opts, optionView{Label: o.Label, Value: o.Value, Selected: o.Selected})
	}
	for {
		ans, err := a.s.wait(questionView{
			ID: id, Kind: "choose", Title: q.Title, Description: q.Description,
			Multiple: q.Multiple, Min: min, Error: msg, Options: opts,
		})
		if err != nil {
			return nil, err
		}
		if len(ans.Values) < min {
			if q.Multiple {
				msg = "Pick at least one."
			} else {
				msg = "Pick one."
			}
			continue
		}
		if !q.Multiple && len(ans.Values) > 1 {
			ans.Values = ans.Values[:1]
		}
		return ans.Values, nil
	}
}

func (a webAsker) Input(q flow.Field) (string, error) {
	id := newID()
	msg := ""
	for {
		ans, err := a.s.wait(questionView{
			ID: id, Kind: "input", Title: q.Title, Description: q.Description,
			Secret: q.Secret, Default: q.Default, Error: msg,
		})
		if err != nil {
			return "", err
		}
		text := strings.TrimSpace(ans.Text)
		if q.Validate != nil {
			if msg = q.Validate(text); msg != "" {
				q.Default = text
				continue
			}
		}
		return text, nil
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

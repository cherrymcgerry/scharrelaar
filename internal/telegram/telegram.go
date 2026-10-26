// Package telegram sends alerts and reads commands. It uses long polling, so the
// user's machine needs no public address.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Update is one incoming message or button press.
type Update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
	Callback *struct {
		ID   string `json:"id"`
		Data string `json:"data"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Message struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// ChatID is the chat the update came from.
func (u Update) ChatID() int64 {
	if u.Message != nil {
		return u.Message.Chat.ID
	}
	if u.Callback != nil {
		return u.Callback.Message.Chat.ID
	}
	return 0
}

// Text is the message text, or "" for a button press.
func (u Update) Text() string {
	if u.Message != nil {
		return u.Message.Text
	}
	return ""
}

// CallbackID identifies a button press for AnswerCallback, or "".
func (u Update) CallbackID() string {
	if u.Callback != nil {
		return u.Callback.ID
	}
	return ""
}

// CallbackData is the button payload, or "".
func (u Update) CallbackData() string {
	if u.Callback != nil {
		return u.Callback.Data
	}
	return ""
}

// Button is one inline keyboard button.
type Button struct {
	Text string
	Data string // callback data; mutually exclusive with URL
	URL  string
}

// Bot talks to the Telegram Bot API.
type Bot struct {
	Token string
	HTTP  *http.Client
	// API overrides the API root in tests.
	API string
}

func (b *Bot) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return &http.Client{Timeout: 70 * time.Second}
}

func (b *Bot) root() string {
	if b.API != "" {
		return b.API
	}
	return "https://api.telegram.org/bot" + b.Token
}

func (b *Bot) call(method string, payload any, out any) error {
	return b.callContext(context.Background(), method, payload, out)
}

func (b *Bot) callContext(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.root()+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("telegram returned %d: %s", resp.StatusCode, truncate(data))
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200])
	}
	return string(b)
}

// Me is the bot's own identity.
type Me struct {
	Username string `json:"username"`
	Name     string `json:"first_name"`
}

// GetMe checks that the token is valid.
func (b *Bot) GetMe() (Me, error) {
	var me Me
	err := b.call("getMe", map[string]any{}, &me)
	return me, err
}

// Send sends a message, optionally with buttons. parseHTML selects the HTML parse mode.
func (b *Bot) Send(chatID int64, text string, buttons [][]Button, parseHTML bool) error {
	payload := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]any{"is_disabled": true}}
	if parseHTML {
		payload["parse_mode"] = "HTML"
	}
	if len(buttons) > 0 {
		payload["reply_markup"] = keyboard(buttons)
	}
	return b.call("sendMessage", payload, nil)
}

// SendPhoto sends a picture with a caption, falling back to a plain message.
func (b *Bot) SendPhoto(chatID int64, photo, caption string, buttons [][]Button) error {
	payload := map[string]any{
		"chat_id": chatID, "photo": photo, "caption": caption, "parse_mode": "HTML",
	}
	if len(buttons) > 0 {
		payload["reply_markup"] = keyboard(buttons)
	}
	if err := b.call("sendPhoto", payload, nil); err != nil {
		return b.Send(chatID, caption, buttons, true)
	}
	return nil
}

func keyboard(rows [][]Button) map[string]any {
	out := make([][]map[string]string, len(rows))
	for i, row := range rows {
		out[i] = make([]map[string]string, len(row))
		for j, btn := range row {
			m := map[string]string{"text": btn.Text}
			if btn.URL != "" {
				m["url"] = btn.URL
			} else {
				m["callback_data"] = btn.Data
			}
			out[i][j] = m
		}
	}
	return map[string]any{"inline_keyboard": out}
}

// Updates fetches updates after offset, waiting up to timeout seconds.
func (b *Bot) Updates(offset, timeout int) ([]Update, error) {
	return b.Poll(context.Background(), offset, timeout)
}

// Poll is Updates, but it returns when ctx is cancelled instead of waiting out
// the long poll. The scanner and setup both read updates, so one has to be able
// to stop promptly and let the other take over.
func (b *Bot) Poll(ctx context.Context, offset, timeout int) ([]Update, error) {
	var updates []Update
	err := b.callContext(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

// AnswerCallback acknowledges a button press so the client's spinner stops.
func (b *Bot) AnswerCallback(id, text string) error {
	return b.call("answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

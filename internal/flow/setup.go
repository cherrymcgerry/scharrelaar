package flow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/telegram"
)

// saveToken stores the bot token. Tests replace it so they never touch the
// keyring of the machine they run on.
var saveToken = config.SetToken

type setupOpts struct {
	skipModel   bool
	telegramAPI string
}

// Setup walks the user through hardware, model, location and Telegram, then saves.
func Setup(ctx context.Context, cfg *config.Config, mgr *llm.Manager, a Asker) error {
	return setup(ctx, cfg, mgr, a, setupOpts{})
}

func setup(ctx context.Context, cfg *config.Config, mgr *llm.Manager, a Asker, opt setupOpts) error {
	a.Progress("Looking at this computer...")
	hw := llm.Probe()
	a.Progress(fmt.Sprintf("Hardware: %s", hw.Summary()))
	recommended, ok := llm.Recommend(hw)
	if !ok {
		a.Progress("No GPU and less than 16 GB of RAM. A local model will be very slow; a remote model comes in a later version.")
	}

	choices := llm.Choices()
	selected := -1
	if cfg.Model != "" {
		for i, m := range choices {
			if m.Name != cfg.Model {
				continue
			}
			selected = i
			if ok && m.Fallback == recommended.Fallback {
				break
			}
		}
	}
	var modelOpts []Option
	for i, m := range choices {
		label := m.String()
		if ok && m.Name == recommended.Name && m.Fallback == recommended.Fallback {
			label += "  (recommended)"
		}
		modelOpts = append(modelOpts, Option{Label: label, Value: strconv.Itoa(i), Selected: i == selected})
	}
	picked, err := a.Choose(Choice{
		Title:   "Which model do you want to run?",
		Options: modelOpts,
	})
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return fmt.Errorf("pick a model")
	}
	index, err := strconv.Atoi(picked[0])
	if err != nil || index < 0 || index >= len(choices) {
		return fmt.Errorf("unknown model choice %q", picked[0])
	}
	cfg.Model = choices[index].Name

	if !opt.skipModel {
		if err := prepareModel(ctx, mgr, cfg.Model, a, hw); err != nil {
			return err
		}
	}
	if err := setupVision(ctx, cfg, mgr, a, hw, opt.skipModel); err != nil {
		return err
	}

	country := cfg.Country
	if country == "" {
		country = "BE"
	}
	postcode := cfg.Postcode
	radius := strconv.Itoa(max(cfg.DistanceM/1000, 25))
	countries := []Option{
		{Label: "Belgium (2dehands.be)", Value: "BE", Selected: country == "BE"},
		{Label: "Netherlands (marktplaats.nl)", Value: "NL", Selected: country == "NL"},
		{Label: "Both", Value: "BOTH", Selected: country == "BOTH"},
	}
	got, err := a.Choose(Choice{Title: "Where do you search?", Options: countries})
	if err != nil {
		return err
	}
	if len(got) == 0 {
		return fmt.Errorf("pick a country")
	}
	country = got[0]
	postcode, err = a.Input(Field{
		Title:   "Your postcode",
		Default: postcode,
		Validate: func(s string) string {
			if len(strings.TrimSpace(s)) < 4 {
				return "enter a postcode"
			}
			return ""
		},
	})
	if err != nil {
		return err
	}
	radius, err = a.Input(Field{
		Title:       "Search radius in km",
		Description: "How far you will travel.",
		Default:     radius,
		Validate: func(s string) string {
			km, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || km <= 0 {
				return "enter a number of kilometres"
			}
			return ""
		},
	})
	if err != nil {
		return err
	}
	cfg.Country = country
	cfg.Postcode = strings.TrimSpace(postcode)
	if km, err := strconv.Atoi(strings.TrimSpace(radius)); err == nil && km > 0 {
		cfg.DistanceM = km * 1000
	}
	switch country {
	case "NL":
		cfg.Sites = []string{"marktplaats"}
	case "BOTH":
		cfg.Sites = []string{"2dehands", "marktplaats"}
	default:
		cfg.Sites = []string{"2dehands"}
	}

	if err := setupTelegram(ctx, cfg, a, opt.telegramAPI); err != nil {
		return err
	}
	if err := config.Save(*cfg); err != nil {
		return err
	}
	a.Progress("Setup complete. Add a search with: scharrel watch add")
	return nil
}

// setupVision offers the photo model, which judges listings by their photos and
// by the brand and model they show.
func setupVision(ctx context.Context, cfg *config.Config, mgr *llm.Manager, a Asker, hw llm.Hardware, skipPull bool) error {
	vision, ok := llm.RecommendVision(hw)
	if !ok {
		cfg.VisionModel = ""
		return nil
	}
	got, err := a.Choose(Choice{
		Title: "Also look at the photos of each listing?",
		Description: "Sellers often leave out what you need, such as the connections on the back or the exact model. " +
			"A second model reads the photos. It is slower per listing.",
		Options: []Option{
			{Label: fmt.Sprintf("Yes, use %s (%d GB download, %s)", vision.Name, vision.SizeGB, vision.Quality), Value: "yes", Selected: true},
			{Label: "No, judge on the text only", Value: "no"},
		},
	})
	if err != nil {
		return err
	}
	if firstOr(got, "no") != "yes" {
		cfg.VisionModel = ""
		return nil
	}
	cfg.VisionModel = vision.Name
	if skipPull || mgr.Has(ctx, vision.Name) {
		return nil
	}
	prev := mgr.Out
	mgr.Out = &lineWriter{a: a}
	defer func() { mgr.Out = prev }()
	a.Progress(fmt.Sprintf("Downloading %s...", vision.Name))
	if err := mgr.Pull(ctx, vision.Name); err != nil {
		a.Progress(fmt.Sprintf("Could not download the photo model (%v); judging on text only. Run setup again to retry.", err))
		cfg.VisionModel = ""
	}
	return nil
}

func prepareModel(ctx context.Context, mgr *llm.Manager, model string, a Asker, hw llm.Hardware) error {
	prev := mgr.Out
	mgr.Out = &lineWriter{a: a}
	defer func() { mgr.Out = prev }()

	if !mgr.Installed() {
		if err := mgr.Download(ctx, hw); err != nil {
			return fmt.Errorf("installing the model server: %w", err)
		}
	}
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	if !mgr.Has(ctx, model) {
		a.Progress(fmt.Sprintf("Downloading %s. This is the slow part.", model))
		if err := mgr.Pull(ctx, model); err != nil {
			return fmt.Errorf("downloading the model: %w", err)
		}
	}
	a.Progress("Checking the model answers correctly...")
	elapsed, err := mgr.SelfTest(ctx, model)
	if err != nil {
		return fmt.Errorf("the model did not respond correctly: %w", err)
	}
	a.Progress(fmt.Sprintf("Model ready (answered in %s).", elapsed.Round(time.Second)))
	return nil
}

func setupTelegram(ctx context.Context, cfg *config.Config, a Asker, api string) error {
	a.Show(Card{
		Title: "Telegram alerts",
		Lines: []string{
			"1. Open @BotFather in Telegram.",
			"2. Send /newbot, pick a name and a username, and copy the token it gives you.",
		},
		Link: "https://t.me/BotFather",
		QR:   "https://t.me/BotFather",
	})
	token, err := a.Input(Field{
		Title:  "Paste the bot token",
		Secret: true,
		Validate: func(s string) string {
			if !strings.Contains(s, ":") {
				return "that does not look like a bot token"
			}
			return ""
		},
	})
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	bot := &telegram.Bot{Token: token, API: api}
	me, err := bot.GetMe()
	if err != nil {
		return fmt.Errorf("that token was rejected: %w", err)
	}
	a.Progress(fmt.Sprintf("Token accepted: @%s", me.Username))

	code := pairingCode()
	link := fmt.Sprintf("https://t.me/%s?start=%s", me.Username, code)
	a.Show(Card{
		Title: "Link this computer",
		Lines: []string{"3. On your phone, open this link and press Start."},
		Link:  link,
		QR:    link,
	})
	a.Progress("Waiting for you to press Start...")

	chatID, err := waitForStart(ctx, bot, code, 5*time.Minute)
	if err != nil {
		return err
	}
	cfg.ChatID = strconv.FormatInt(chatID, 10)
	if err := saveToken(token); err != nil {
		return fmt.Errorf("saving the token: %w", err)
	}
	if err := bot.Send(chatID, "Paired! Scharrelaar will send deals here.", nil, false); err != nil {
		return fmt.Errorf("sending the confirmation: %w", err)
	}
	a.Progress("Paired.")
	return nil
}

// waitForStart reads updates until one carries /start <code> and returns its chat.
func waitForStart(ctx context.Context, bot *telegram.Bot, code string, timeout time.Duration) (int64, error) {
	deadline := time.Now().Add(timeout)
	offset := 0
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		updates, err := bot.Poll(ctx, offset, 20)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, err
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if strings.TrimSpace(u.Text()) == "/start "+code {
				return u.ChatID(), nil
			}
		}
	}
	return 0, fmt.Errorf("no Start press received within %s", timeout)
}

// pairingCode is the secret in the /start link: whoever sends it becomes the chat
// the bot obeys, so it must not be guessable.
func pairingCode() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// lineWriter forwards whole lines to the asker, for model download progress.
type lineWriter struct {
	a   Asker
	buf bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		b := w.buf.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(b[:i]), "\r")
		w.buf.Next(i + 1)
		if line != "" {
			w.a.Progress(line)
		}
	}
	return len(p), nil
}

package telegram

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/score"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

func TestGetMeAndSend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			w.Write([]byte(`{"ok":true,"result":{"username":"scharrel_bot","first_name":"Scharrel"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			w.Write([]byte(`{"ok":false,"description":"unknown"}`))
		}
	}))
	defer srv.Close()

	b := &Bot{Token: "t", API: srv.URL}
	me, err := b.GetMe()
	if err != nil || me.Username != "scharrel_bot" {
		t.Fatalf("getMe: %+v %v", me, err)
	}
	err = b.Send(42, "hallo", [][]Button{{{Text: "Open", URL: "https://x"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got["chat_id"].(float64) != 42 {
		t.Errorf("chat %v", got["chat_id"])
	}
	kb := got["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	btn := kb[0].([]any)[0].(map[string]any)
	if btn["url"] != "https://x" {
		t.Errorf("button %v", btn)
	}
}

func TestUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"message":{"text":"/status","chat":{"id":5}}},{"update_id":8,"callback_query":{"id":"c","data":"fb:relevant:m1","message":{"chat":{"id":5}}}}]}`))
	}))
	defer srv.Close()
	b := &Bot{Token: "t", API: srv.URL}
	ups, err := b.Updates(1, 0)
	if err != nil || len(ups) != 2 {
		t.Fatalf("%+v %v", ups, err)
	}
	if ups[0].Text() != "/status" || ups[0].ChatID() != 5 {
		t.Errorf("message %+v", ups[0])
	}
	if ups[1].CallbackData() != "fb:relevant:m1" || ups[1].ChatID() != 5 {
		t.Errorf("callback %+v", ups[1])
	}
}

func TestFormatRepair(t *testing.T) {
	a := Alert{
		Watch:   watch.Watch{Name: "audio", Mode: watch.ModeRepair},
		Listing: market.Listing{Title: "Philips versterker", PriceCents: 1500, PriceType: "MIN_BID", City: "Gent", DistanceM: 32000, URL: "https://x"},
		Eval: score.Eval{BrandModel: "Philips 22RH", FaultQuote: "werkt niet meer", FaultIsSpecific: true,
			FaultCategory: "POWER", Difficulty: "MEDIUM", Confidence: "MEDIUM", LearningValue: "MEDIUM",
			SkillLearned: "amplifier power supply", PartsCostEUR: 5, WorkingValueEUR: 40, PerceivedIssue: "no power"},
		Deal:      score.Deal{Score: 2, LearningScore: 8, BuyEUR: 15, BuyIsEstimate: true, OutlayEUR: 20, ProfitEUR: 20},
		DealAlert: false,
	}
	got := Format(a)
	for _, want := range []string{"leerproject audio 8/10", "Uitgave:", "Leert: amplifier power supply", "werkt niet meer", "Gent (32 km)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestFormatBuy(t *testing.T) {
	a := Alert{
		Watch:   watch.Watch{Name: "trailer", Mode: watch.ModeBuy},
		Listing: market.Listing{Title: "Anssems GT750", PriceCents: 65000, PriceType: "FIXED", City: "Aalst", DistanceM: 20000},
		Eval: score.Eval{Requirements: []score.Requirement{
			{Name: "braked", Status: "met", Quote: "geremd, 750 kg"},
			{Name: "bed length >= 2.5 m", Status: "unknown"},
		}},
		Deal: score.Deal{Score: 8, MedianEUR: 780, Questions: []string{"bed length >= 2.5 m"}},
	}
	got := Format(a)
	for _, want := range []string{"trailer 8/10", "mediaan €780", "v braked", "? bed length", "Vraag de verkoper"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(Buttons(a)) != 2 {
		t.Error("expected two button rows")
	}
}

func TestFeedbackData(t *testing.T) {
	a := Alert{Watch: watch.Watch{Name: "trailer"}, Listing: market.Listing{ItemID: "m2458063944", URL: "https://x"}}
	rows := Buttons(a)
	data := rows[1][1].Data
	verdict, w, item, ok := ParseFeedback(data)
	if !ok || verdict != "not_relevant" || w != "trailer" || item != "m2458063944" {
		t.Fatalf("%q decoded to %q %q %q %v", data, verdict, w, item, ok)
	}
	if len(data) > 64 {
		t.Fatalf("callback data %d bytes", len(data))
	}
	if _, _, _, ok := ParseFeedback("fb:relevant:m1"); ok {
		t.Error("old-style data accepted")
	}
}

func TestCaptionKeepsHTMLIntact(t *testing.T) {
	long := strings.Repeat("&lt;b&gt; ", 60)
	lines := []string{"<b>header</b>", "<i>" + long + "</i>", "<i>" + long + "</i>", "<i>" + long + "</i>"}
	got := fitLines(lines, "Gent | ophalen", 1024)
	if len([]rune(got)) > 1024 || !strings.HasSuffix(got, "Gent | ophalen") {
		t.Fatalf("caption %d chars:\n%s", len([]rune(got)), got)
	}
	if strings.Count(got, "<i>") != strings.Count(got, "</i>") {
		t.Fatalf("unbalanced tags:\n%s", got)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer srv.Close()
	b := &Bot{Token: "bad", API: srv.URL}
	if _, err := b.GetMe(); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("got %v", err)
	}
}

package telegram

import (
	"fmt"
	"html"
	"strings"

	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/score"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

const captionLimit = 1024

// Alert describes one listing worth sending.
type Alert struct {
	Watch   watch.Watch
	Listing market.Listing
	Eval    score.Eval
	Deal    score.Deal
	// DealAlert is true when the deal score (rather than the learning score) triggered it.
	DealAlert bool
}

// Format renders an alert as a Telegram HTML caption.
func Format(a Alert) string {
	l, ev, d := a.Listing, a.Eval, a.Deal
	e := html.EscapeString
	name := ev.BrandModel
	if name == "" || strings.Contains(strings.ToLower(name), "unknown") {
		name = l.Title
	}
	where := l.City
	if where == "" {
		where = "?"
	}
	if l.DistanceM >= 0 {
		where += fmt.Sprintf(" (%d km)", l.DistanceM/1000)
	}

	var header string
	switch {
	case a.Watch.Mode == watch.ModeBuy:
		header = fmt.Sprintf("<b>%s %d/10</b>", e(a.Watch.Name), d.Score)
	case a.DealAlert:
		header = fmt.Sprintf("<b>%s deal %d/10</b> | leer %d/10", e(a.Watch.Name), d.Score, d.LearningScore)
	default:
		header = fmt.Sprintf("<b>leerproject %s %d/10</b> | deal %d/10", e(a.Watch.Name), d.LearningScore, d.Score)
	}

	lines := []string{
		header + " - " + e(clip(name, 80)),
		"<i>" + e(clip(l.Title, 120)) + "</i>",
		"",
		fmt.Sprintf("Vraagprijs: %s", e(l.PriceLabel())),
	}
	if l.HighestBid > 0 {
		lines = append(lines, fmt.Sprintf("Biedingen: %d, hoogste €%.0f", l.BidCount, float64(l.HighestBid)/100))
	}

	if a.Watch.Mode == watch.ModeBuy {
		if d.MedianEUR > 0 {
			lines = append(lines, fmt.Sprintf("Vergelijkbare advertenties: mediaan €%.0f", d.MedianEUR))
		}
		for _, r := range ev.Requirements {
			if r.Name == watch.ItemRequirement {
				continue
			}
			mark := "?"
			switch r.Status {
			case "met":
				mark = "v"
			case "unmet":
				mark = "x"
			}
			line := fmt.Sprintf("%s %s", mark, e(r.Name))
			switch {
			case r.Basis == score.BasisPhoto && r.Reason != "":
				line += fmt.Sprintf(" (foto: %s)", e(clip(r.Reason, 100)))
			case r.Basis == score.BasisWeb && r.Reason != "":
				line += fmt.Sprintf(" (web: %s)", e(clip(r.Reason, 100)))
			case r.Quote != "":
				line += fmt.Sprintf(" (\"%s\")", e(clip(r.Quote, 80)))
			}
			lines = append(lines, line)
		}
		if len(d.Questions) > 0 {
			lines = append(lines, "Vraag de verkoper: "+e(strings.Join(d.Questions, ", ")))
		}
	} else {
		buy := fmt.Sprintf("€%.0f", d.BuyEUR)
		if d.BuyIsEstimate {
			buy = "~" + buy + " (geschat)"
		}
		lines = append(lines,
			fmt.Sprintf("Uitgave: ~€%.0f (aankoop %s + onderdelen ~€%.0f)", d.OutlayEUR, buy, ev.PartsCostEUR),
			fmt.Sprintf("Werkend: ~€%.0f | Winst: ~€%.0f", ev.WorkingValueEUR, d.ProfitEUR),
			fmt.Sprintf("Max bod: €%.0f", d.MaxBidEUR),
			fmt.Sprintf("Type: %s | Moeilijkheid: %s | Zekerheid: %s", ev.FaultCategory, ev.Difficulty, ev.Confidence),
		)
		if ev.SkillLearned != "" && !strings.EqualFold(ev.SkillLearned, "unknown") {
			lines = append(lines, fmt.Sprintf("Leert: %s (%s)", e(clip(ev.SkillLearned, 120)), ev.LearningValue))
		}
		if ev.FaultQuote != "" {
			lines = append(lines, fmt.Sprintf("Verkoper: \"%s\"", e(clip(ev.FaultQuote, 200))))
		}
		if ev.FaultIsSpecific {
			lines = append(lines, "Defect: "+e(clip(ev.PerceivedIssue, 150)))
		} else {
			lines = append(lines, "Defect: niet gespecificeerd, vraag de verkoper wat er mis is")
		}
		if len(ev.RedFlags) > 0 {
			lines = append(lines, "Let op: "+e(clip(strings.Join(ev.RedFlags, ", "), 150)))
		}
	}
	location := e(where) + " | " + e(orDefault(l.Delivery, "?"))
	return fitLines(lines, location, captionLimit)
}

// fitLines joins lines, dropping whole lines from the end until the text and the
// final line fit in limit characters. Cutting by line keeps HTML tags and
// entities intact.
func fitLines(lines []string, last string, limit int) string {
	for len(lines) > 1 {
		text := strings.Join(append(lines[:len(lines):len(lines)], last), "\n")
		if len([]rune(text)) <= limit {
			return text
		}
		lines = lines[:len(lines)-1]
	}
	return strings.Join(append(lines, last), "\n")
}

// Verdict codes in button data. Telegram limits callback data to 64 bytes.
var verdictCodes = map[string]string{"r": "relevant", "n": "not_relevant", "e": "too_expensive"}

// Buttons builds the buttons under an alert.
func Buttons(a Alert) [][]Button {
	rows := [][]Button{{{Text: "Open advertentie", URL: a.Listing.URL}}}
	data := func(code string) string { return "fb:" + code + ":" + a.Watch.Name + ":" + a.Listing.ItemID }
	if len(data("e")) <= 64 {
		rows = append(rows, []Button{
			{Text: "Relevant", Data: data("r")},
			{Text: "Niet relevant", Data: data("n")},
			{Text: "Te duur", Data: data("e")},
		})
	}
	return rows
}

// ParseFeedback decodes the data of a feedback button.
func ParseFeedback(data string) (verdict, watchName, itemID string, ok bool) {
	parts := strings.SplitN(data, ":", 4)
	if len(parts) != 4 || parts[0] != "fb" {
		return "", "", "", false
	}
	verdict, ok = verdictCodes[parts[1]]
	return verdict, parts[2], parts[3], ok && parts[2] != "" && parts[3] != ""
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimRight(string(r[:limit-1]), " ") + "…"
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cherrymcgerry/scharrelaar/internal/score"
	"github.com/cherrymcgerry/scharrelaar/internal/store"
	"github.com/cherrymcgerry/scharrelaar/internal/watch"
)

// handleListings shows everything one search has seen: the listing, what the
// model found, and why an alert was or was not sent.
func (s *Server) handleListings(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "listings are recorded once Scharrelaar is running")
		return
	}
	name := r.PathValue("name")
	rows, err := s.Store.Listings(name, 400)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	activity, err := s.Store.Activity(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if activity == nil {
		activity = map[string]int{}
	}
	body := map[string]any{
		"name":     name,
		"total":    activity["total"],
		"alerted":  activity["alerted"],
		"judged":   activity[store.StatusEvaluated],
		"skipped":  activity[store.StatusSkipped],
		"recorded": activity[store.StatusSeeded],
		"errors":   activity[store.StatusError],
		"shown":    len(rows),
	}
	if item, err := watch.Load(name); err == nil {
		body["description"] = item.Description
		body["must_have"] = item.MustHave
		body["nice_to_have"] = item.NiceToHave
		var checks []map[string]string
		for _, c := range item.Checks {
			checks = append(checks, map[string]string{"wish": c.Wish, "means": c.Title()})
		}
		body["checks"] = checks
		body["mode"] = item.Mode
		body["detail"] = watchDetail(item)
	}
	listings := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		listings = append(listings, listingJSON(row))
	}
	body["listings"] = listings
	writeJSON(w, http.StatusOK, body)
}

func listingJSON(row store.Row) map[string]any {
	findings, notes, questions, repair := parseJudgement(row.EvalJSON)
	out := map[string]any{
		"title":          row.Title,
		"price":          money(row.PriceCents, row.PriceType),
		"url":            row.URL,
		"site":           row.Site,
		"status":         row.Status,
		"decision":       explain(row),
		"score":          row.Score,
		"learning_score": row.LearningScore,
		"alerted":        row.Notified,
		"last_seen":      row.LastSeen,
	}
	if len(findings) > 0 {
		out["findings"] = findings
	}
	ev := evidence(row.EvalJSON)
	if ev.PhotosSeen > 0 {
		out["photos_seen"] = ev.PhotosSeen
		out["seen"] = ev.Seen
	}
	if ev.LookedUp != "" {
		out["looked_up"] = ev.LookedUp
	}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	if len(questions) > 0 {
		out["questions"] = questions
	}
	if len(repair) > 0 {
		out["repair"] = repair
	}
	return out
}

// explain turns a stored row into the sentence shown for its alert decision.
// Rows written before decisions were stored get the same sentences from their status.
func explain(row store.Row) string {
	reason := strings.TrimSpace(row.Reason)
	switch {
	case row.Status == store.StatusSeeded:
		return "Already listed when this search was created. Waiting to be judged; a few are judged every scan."
	case strings.HasPrefix(reason, "Alerted"), strings.HasPrefix(reason, "Not alerted"):
		return reason
	case row.Status == store.StatusSkipped && reason != "":
		return "Not alerted: " + reason
	case row.Status == store.StatusSkipped:
		return "Not alerted."
	case row.Status == store.StatusError && reason != "":
		return "Could not be judged: " + reason
	case row.Status == store.StatusError:
		return "Could not be judged."
	case row.Notified:
		return fmt.Sprintf("Alerted. Deal score %d.", row.Score)
	case row.Status == store.StatusEvaluated:
		return fmt.Sprintf("Not alerted. Deal score %d.", row.Score)
	default:
		if reason != "" {
			return reason
		}
		return row.Status
	}
}

type finding struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Quote  string `json:"quote,omitempty"`
	Basis  string `json:"basis,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// evidence returns what a listing was judged on besides its text: the photos
// looked at and what they showed, and the model looked up on the web.
func evidence(raw string) score.Eval {
	var ev score.Eval
	json.Unmarshal([]byte(raw), &ev)
	return ev
}

func parseJudgement(raw string) (findings []finding, notes, questions []string, repair map[string]string) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, nil, nil
	}
	var body struct {
		Requirements []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Quote  string `json:"quote"`
			Basis  string `json:"basis"`
			Reason string `json:"reason"`
		} `json:"requirements"`
		Notes          []string `json:"notes"`
		Questions      []string `json:"questions"`
		FaultQuote     string   `json:"fault_quote"`
		PerceivedIssue string   `json:"perceived_issue"`
		LikelyCause    string   `json:"likely_cause"`
		Difficulty     string   `json:"repair_difficulty"`
		LearningValue  string   `json:"learning_value"`
		Reasoning      string   `json:"reasoning"`
		FaultCategory  string   `json:"fault_category"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return nil, nil, nil, nil
	}
	for _, req := range body.Requirements {
		if req.Name == "" {
			continue
		}
		status := req.Status
		if status == "" {
			status = "unknown"
		}
		findings = append(findings, finding{Name: req.Name, Status: status, Quote: req.Quote, Basis: req.Basis, Reason: req.Reason})
	}
	repair = map[string]string{}
	put := func(key, value string) {
		if strings.TrimSpace(value) != "" && value != "unknown" && value != "UNSPECIFIED" {
			repair[key] = value
		}
	}
	put("fault", body.FaultQuote)
	put("issue", body.PerceivedIssue)
	put("likely cause", body.LikelyCause)
	put("difficulty", body.Difficulty)
	put("learning value", body.LearningValue)
	put("fault category", body.FaultCategory)
	put("reasoning", body.Reasoning)
	if len(repair) == 0 {
		repair = nil
	}
	return findings, body.Notes, body.Questions, repair
}

func money(cents int, typ string) string {
	euros := float64(cents) / 100
	switch typ {
	case "FIXED":
		return fmt.Sprintf("€%.0f", euros)
	case "MIN_BID":
		return fmt.Sprintf("bieden vanaf €%.0f", euros)
	case "FREE":
		return "gratis"
	case "FAST_BID":
		return "bieden"
	case "SEE_DESCRIPTION":
		return "zie omschrijving"
	case "RESERVED":
		return "gereserveerd"
	default:
		if cents > 0 {
			return fmt.Sprintf("€%.0f", euros)
		}
		return typ
	}
}

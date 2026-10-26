// Package score turns a model's evaluation of a listing into numbers. The model never
// does arithmetic; all prices, scores and gates live here so they stay testable and
// identical no matter which model produced the evaluation.
package score

import (
	"math"
	"strings"
)

// Difficulty and confidence values the model may return.
const (
	Easy   = "EASY"
	Medium = "MEDIUM"
	Hard   = "HARD"
	Avoid  = "AVOID"

	Low    = "LOW"
	High   = "HIGH"
	Unspec = "UNSPECIFIED"
)

// Eval is what the model fills in. Money fields are estimates in euros.
type Eval struct {
	BrandModel      string   `json:"brand_model"`
	DeviceType      string   `json:"device_type"`
	IsQualityDevice bool     `json:"is_quality_device"`
	HasFault        bool     `json:"has_fault"`
	FaultQuote      string   `json:"fault_quote"`
	FaultIsSpecific bool     `json:"fault_is_specific"`
	FaultCategory   string   `json:"fault_category"`
	PerceivedIssue  string   `json:"perceived_issue"`
	LikelyCause     string   `json:"likely_cause"`
	Difficulty      string   `json:"repair_difficulty"`
	PartsCostEUR    float64  `json:"estimated_parts_cost_eur"`
	WorkingValueEUR float64  `json:"estimated_working_value_eur"`
	Confidence      string   `json:"confidence"`
	RedFlags        []string `json:"red_flags"`
	LearningValue   string   `json:"learning_value"`
	SkillLearned    string   `json:"skill_learned"`
	Reasoning       string   `json:"reasoning"`

	// Buy mode: one verdict per requirement, in the same order as the watch's
	// requirements; Name is always the watch's own wording. Status is "met",
	// "unmet" or "unknown".
	Requirements []Requirement `json:"requirements"`
	// LikeRejected is the model's judgement that the listing resembles one the
	// user marked not relevant.
	LikeRejected bool `json:"like_rejected,omitempty"`
	// PhotosSeen is how many of the listing's photos the model looked at.
	PhotosSeen int `json:"photos_seen,omitempty"`
	// Seen is what the photo model described on them, before it knew what to look
	// for.
	Seen string `json:"seen,omitempty"`
	// LookedUp is the brand and model that was looked up on the web.
	LookedUp string `json:"looked_up,omitempty"`
}

// Requirement is the model's verdict on one thing the buyer asked for.
type Requirement struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Quote  string `json:"quote"`
	// Basis is where the verdict comes from: BasisText, BasisPhoto or BasisWeb.
	Basis string `json:"basis,omitempty"`
	// Reason is what the photos or web pages show, for those verdicts.
	Reason string `json:"reason,omitempty"`
}

// Where a requirement verdict comes from.
const (
	BasisText  = "ad text"
	BasisPhoto = "photo"
	BasisWeb   = "web"
)

// Deal is the result of scoring one listing.
type Deal struct {
	BuyEUR        float64
	BuyIsEstimate bool
	ProfitEUR     float64
	MaxBidEUR     float64
	OutlayEUR     float64
	Score         int
	LearningScore int
	Notes         []string
	// Buy mode.
	RequirementsMet int
	Requirements    int
	MedianEUR       float64
	Questions       []string
}

// RepairParams are the thresholds for flip/repair scoring.
type RepairParams struct {
	MinProfitEUR    float64
	MinMarginEUR    float64
	AssumedBidRatio float64
	// AllowedFaults limits which fault categories score; nil allows all.
	AllowedFaults map[string]bool
}

// DefaultRepair are the thresholds the audio bot shipped with.
func DefaultRepair() RepairParams {
	return RepairParams{MinProfitEUR: 40, MinMarginEUR: 50, AssumedBidRatio: 0.25}
}

// ListingFacts is the part of a listing the scoring needs.
type ListingFacts struct {
	PriceEUR   float64
	PriceType  string
	HighestBid float64 // euros, 0 when unknown
}

var (
	difficultyFactor = map[string]float64{Easy: 1, Medium: 0.75, Hard: 0.4, Avoid: 0}
	confidenceBonus  = map[string]float64{Low: -1, Medium: 0, High: 1}
	learningBase     = map[string]float64{Low: 2, Medium: 5, High: 7}
)

// Repair scores a flip/repair evaluation: a deal score (profit driven) and a learning
// score (how good a practice project it is).
func Repair(l ListingFacts, ev Eval, p RepairParams) Deal {
	buy, estimate := buyPrice(l, ev.WorkingValueEUR, p.AssumedBidRatio)
	profit := ev.WorkingValueEUR - ev.PartsCostEUR - buy
	d := Deal{
		BuyEUR: buy, BuyIsEstimate: estimate, ProfitEUR: profit,
		MaxBidEUR: math.Max(0, ev.WorkingValueEUR-ev.PartsCostEUR-p.MinMarginEUR),
		OutlayEUR: buy + ev.PartsCostEUR,
	}

	inScope := p.AllowedFaults == nil || p.AllowedFaults[ev.FaultCategory]
	switch {
	case !ev.HasFault:
		d.Score = 1
		d.Notes = append(d.Notes, "no fault described")
	case !inScope:
		d.Score = 1
		d.Notes = append(d.Notes, "fault "+ev.FaultCategory+" out of scope")
	case ev.Difficulty == Avoid:
		d.Score = 1
		d.Notes = append(d.Notes, "difficulty AVOID")
	case profit < p.MinProfitEUR:
		d.Score = 2
		d.Notes = append(d.Notes, "profit below minimum")
	default:
		roi := profit / math.Max(buy+ev.PartsCostEUR, 10)
		raw := math.Min(profit/30, 5) + math.Min(roi*2, 3)
		raw = raw*difficultyFactor[ev.Difficulty] + confidenceBonus[ev.Confidence]
		if !ev.IsQualityDevice {
			raw -= 2
			d.Notes = append(d.Notes, "not a quality device")
		}
		if len(ev.RedFlags) > 0 {
			raw--
		}
		d.Score = clamp(int(math.Round(raw)))
	}
	d.LearningScore = learning(ev, buy, profit, inScope)
	return d
}

func learning(ev Eval, buy, profit float64, inScope bool) int {
	if !ev.HasFault || !inScope || ev.Difficulty == Avoid {
		return 1
	}
	raw := learningBase[ev.LearningValue]
	if ev.Difficulty == Medium || ev.Difficulty == Hard {
		raw++
	}
	if ev.FaultIsSpecific {
		raw++
	} else {
		raw -= 2
	}
	outlay := buy + ev.PartsCostEUR
	switch {
	case outlay <= 25:
		raw++
	case outlay > 100:
		raw--
	}
	if profit >= 0 {
		raw++
	} else if -profit > outlay/2 {
		raw -= 2
	}
	if len(ev.RedFlags) > 0 {
		raw--
	}
	return clamp(int(raw))
}

func buyPrice(l ListingFacts, value, ratio float64) (float64, bool) {
	switch l.PriceType {
	case "FIXED":
		return l.PriceEUR, false
	case "FREE":
		return 0, false
	case "MIN_BID":
		return math.Max(l.PriceEUR, value*ratio), true
	default:
		guess := value * ratio
		if l.HighestBid > 0 {
			guess = math.Max(guess, l.HighestBid*1.1)
		}
		return guess, true
	}
}

// GroundRepair discards claims the model cannot back up with the seller's own words.
// text is the listing title plus description.
func GroundRepair(ev *Eval, text string) {
	if ev.FaultQuote != "" && !containsNormalized(text, ev.FaultQuote) {
		ev.FaultQuote = ""
	}
	if ev.FaultQuote == "" {
		ev.FaultIsSpecific = false
	}
	if !ev.FaultIsSpecific {
		ev.FaultCategory = Unspec
		ev.PerceivedIssue = "Unspecified fault"
		ev.LikelyCause = "unknown"
		ev.Confidence = Low
		if ev.Difficulty == Easy || (ev.Difficulty == Avoid && ev.IsQualityDevice) {
			ev.Difficulty = Medium
		}
		if ev.LearningValue == High {
			ev.LearningValue = Medium
		}
		ev.SkillLearned = "unknown"
	}
}

// BuyParams are the thresholds for buy-mode scoring.
type BuyParams struct {
	// Hard marks which requirements reject a listing when unmet, by position in
	// Eval.Requirements.
	Hard []bool
	// MedianEUR is the median asking price of comparable listings; 0 if unknown.
	MedianEUR float64
}

// Buy scores a "find me one of these" evaluation. An unmet hard requirement scores 1.
// Otherwise the score combines how many requirements are met with the price against
// the median of comparable listings. A watch without requirements is scored on
// price alone.
func Buy(l ListingFacts, ev Eval, p BuyParams) Deal {
	d := Deal{MedianEUR: p.MedianEUR, Requirements: len(ev.Requirements)}
	buy, estimate := buyPrice(l, p.MedianEUR, 1)
	d.BuyEUR, d.BuyIsEstimate = buy, estimate
	if p.MedianEUR > 0 {
		d.ProfitEUR = p.MedianEUR - buy
	}

	unknown, unmetHard := 0, false
	for i, r := range ev.Requirements {
		switch r.Status {
		case "met":
			d.RequirementsMet++
		case "unmet":
			if i < len(p.Hard) && p.Hard[i] {
				unmetHard = true
			}
		default:
			unknown++
			d.Questions = append(d.Questions, r.Name)
		}
	}
	if unmetHard {
		d.Score = 1
		d.Notes = append(d.Notes, "hard requirement unmet")
		return d
	}
	// An unknown requirement counts half: the alert still goes out, with the
	// question for the seller, but below a listing that confirms it.
	match := 1.0
	if n := len(ev.Requirements); n > 0 {
		match = (float64(d.RequirementsMet) + 0.5*float64(unknown)) / float64(n)
	}
	price := 5.0
	if p.MedianEUR > 0 && buy > 0 {
		price = math.Min(10, math.Max(0, 10*(p.MedianEUR-buy)/p.MedianEUR+5))
	}
	raw := match*6 + price*0.4
	if unknown > 0 {
		raw -= 0.5
	}
	d.Score = clamp(int(math.Round(raw)))
	return d
}

// GroundBuy turns verdicts without real evidence into "unknown". A text verdict
// must quote the seller's own words; a photo or web verdict must quote its
// source in sources (the description of the photos, the search results) and
// say what it shows. That an ad does not mention something is no evidence:
// sellers rarely write what a buyer asks for.
func GroundBuy(ev *Eval, text string, sources map[string]string) {
	for i := range ev.Requirements {
		r := &ev.Requirements[i]
		if r.Status != "met" && r.Status != "unmet" {
			continue
		}
		source, other := text, r.Basis != BasisText && r.Basis != ""
		if other {
			source = sources[r.Basis]
		}
		grounded := len(normalize(r.Quote)) >= 3 && containsNormalized(source, r.Quote)
		if other && r.Reason == "" {
			grounded = false
		}
		if !grounded {
			r.Status, r.Quote, r.Basis, r.Reason = "unknown", "", "", ""
		}
	}
}

func containsNormalized(haystack, needle string) bool {
	return strings.Contains(normalize(haystack), normalize(needle))
}

func normalize(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prev = false
			continue
		}
		if !prev {
			b.WriteByte(' ')
			prev = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Median returns the median of the values, or 0 when there are none.
func Median(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	s := append([]float64(nil), values...)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func clamp(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

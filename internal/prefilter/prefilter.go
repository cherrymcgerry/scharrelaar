// Package prefilter applies cheap text rules before a listing is sent to the model.
package prefilter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rules are the text checks for one watch.
type Rules struct {
	// MaxPriceEUR rejects fixed and minimum-bid listings above this price. 0 disables it.
	MaxPriceEUR float64
	// RejectPhrases reject a listing outright ("scherm kapot").
	RejectPhrases []string
	// FaultWords are words that show the listing describes a fault. When RequireFault
	// is set, a listing whose condition is not ConditionFault must contain one.
	FaultWords   []string
	RequireFault bool
	// ConditionFault is the condition value that already proves a fault ("Niet werkend").
	ConditionFault string
	// WantedTerms mark buy-up ads ("gezocht", "ik koop") anywhere in the title.
	WantedTerms []string
	// NegativePhrases are stripped before matching, so "geen defect" does not count as
	// "defect". Longest phrases must come first.
	NegativePhrases []string
}

// DefaultNegatives removes negations that would otherwise look like faults.
var DefaultNegatives = []string{
	"heeft geen enkel defect", "geen enkel defect", "zonder enige defect", "zonder defecten",
	"zonder defect", "geen defecten", "geen defect", "100% werkend", "volledig werkend",
	"alles werkt perfect", "alles werkt prima", "scherm is niet kapot", "scherm niet kapot",
	"geen barsten", "geen barst", "zonder barsten", "zonder barst", "geen waterschade",
	"zonder waterschade", "geen icloud lock", "geen icloud", "icloud vrij", "niet icloud locked",
	"geen bios wachtwoord", "geen bios paswoord", "zonder problemen", "zonder probleem",
	"geen problemen", "geen probleem",
}

// DefaultWanted are the buy-up terms.
var DefaultWanted = []string{"gezocht", "gevraagd", "ik koop", "opkoper", "koop defecte", "cherche"}

// DefaultFaultWords are shared words that indicate a fault.
var DefaultFaultWords = []string{
	"defect", "kapot", "werkt niet", "niet werkend", "voor onderdelen", "wisselstukken",
	"te herstellen", "herstelproject", "reparatie", "probleem", "opknapper", "ongetest", "hapert",
	"pour pièces", "à réparer", "a reparer",
}

// Listing is the part of a listing the rules look at.
type Listing struct {
	Title       string
	Description string
	PriceEUR    float64
	PriceType   string
	Condition   string
}

// Reason returns why a listing is skipped, or "" when it should be evaluated.
func Reason(l Listing, r Rules) string {
	title := strings.ToLower(l.Title)
	for _, term := range r.WantedTerms {
		if strings.Contains(title, term) {
			return "wanted ad"
		}
	}
	if l.PriceType == "RESERVED" {
		return "reserved"
	}
	if r.MaxPriceEUR > 0 && (l.PriceType == "FIXED" || l.PriceType == "MIN_BID") && l.PriceEUR > r.MaxPriceEUR {
		return "price above max"
	}

	text := title + "\n" + strings.ToLower(l.Description)
	for _, phrase := range r.NegativePhrases {
		text = strings.ReplaceAll(text, phrase, " ")
	}
	for _, phrase := range r.RejectPhrases {
		if strings.Contains(text, phrase) {
			return "says '" + phrase + "'"
		}
	}
	if r.RequireFault && l.Condition != r.ConditionFault {
		found := false
		for _, word := range r.FaultWords {
			if strings.Contains(text, word) {
				found = true
				break
			}
		}
		if !found {
			return "no fault mentioned"
		}
	}
	return ""
}

// HasTerm reports whether text contains term as a word, allowing a Dutch plural or
// inflection ending (s, en, e): "speaker" matches "speakers", "tv" matches "tv's",
// but "tas" does not match "fantastisch" and "tv" not "tvbox". Matching ignores case.
func HasTerm(text, term string) bool {
	return hasTerm(text, term, false)
}

// HasCompoundTerm is HasTerm that also accepts term as the end of a Dutch compound
// word: "vaatwasser" matches "inbouwvaatwasser". Terms shorter than five letters
// still need a word boundary, so "tv" does not match "smarttv" or worse.
func HasCompoundTerm(text, term string) bool {
	return hasTerm(text, term, utf8.RuneCountInString(strings.TrimSpace(term)) >= 5)
}

func hasTerm(text, term string, compound bool) bool {
	text, term = strings.ToLower(text), strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return false
	}
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], term)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(term)
		from = start + 1
		if before, _ := utf8.DecodeLastRuneInString(text[:start]); !compound && start > 0 && unicode.IsLetter(before) {
			continue
		}
		rest := text[end:]
		for _, suffix := range []string{"", "s", "en", "e"} {
			if !strings.HasPrefix(rest, suffix) {
				continue
			}
			after, _ := utf8.DecodeRuneInString(rest[len(suffix):])
			if len(rest) == len(suffix) || !unicode.IsLetter(after) {
				return true
			}
		}
	}
	return false
}

// HasAnyTerm reports whether text contains one of the terms as a word.
func HasAnyTerm(text string, terms []string) bool {
	for _, t := range terms {
		if HasTerm(text, t) {
			return true
		}
	}
	return false
}

// HasAnyCompoundTerm reports whether text contains one of the terms as a word or
// at the end of a compound word.
func HasAnyCompoundTerm(text string, terms []string) bool {
	for _, t := range terms {
		if HasCompoundTerm(text, t) {
			return true
		}
	}
	return false
}

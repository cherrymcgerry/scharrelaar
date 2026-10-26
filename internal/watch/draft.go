package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/prefilter"
)

// Building a watch from a description is a short interview:
//
//  1. Understand: the model reads the description and proposes the names sellers
//     might use for the item, in Dutch, English and French.
//  2. Explore: the marketplace says where each name is listed. Names that mean
//     something else there ("trekwagen" is a toy) drop out, and the subcategories
//     the names land in become the choices.
//  3. Ask: the buyer picks the subcategory and answers what is still open, with
//     options taken from the titles of real listings.
//  4. Build: a subcategory that holds mostly this kind of item is searched as a
//     whole, so every wording is covered; otherwise the names that work are searched
//     inside it.

// Intent is what the model understood from the buyer's description.
type Intent struct {
	Item       string   `json:"item"`
	Names      []string `json:"names"`
	MaxPrice   float64  `json:"max_price_eur"`
	RadiusM    int      `json:"radius_m"`
	Place      string   `json:"place"`
	Postcode   string   `json:"postcode"`
	MustHave   []string `json:"must_have"`
	NiceToHave []string `json:"nice_to_have"`
	Mode       string   `json:"mode"`
	Summary    string   `json:"summary"`
}

// Understand reads a free-text description.
func Understand(ctx context.Context, m *llm.Manager, model, description string, opt llm.Options) (Intent, error) {
	system := `You help a buyer search the Belgian and Dutch classifieds sites 2dehands.be and marktplaats.nl. Read what they want and reply with JSON:
- item: the kind of item they want, in one or two Dutch words, without its properties (those go in must_have).
- names: 3 to 8 words a seller could put in an ad title for that kind of item: the Flemish and the Netherlands Dutch word, synonyms, the English and the French word, and common spellings. Only names for the item itself; no parts, brands, properties or prices.
- max_price_eur: their budget, or 0 if they gave none.
- radius_m: how far they will travel in metres, or 0 if they did not say.
- place: the town they want to search around, exactly as they wrote it, or "" if they named none.
- postcode: the postcode of that town, or "" if they named none.
- must_have: what the item must have, in the buyer's own words, a few words each. Empty if they stated nothing; never add your own.
- nice_to_have: what they said they prefer but do not require ("liefst", "bij voorkeur", "preferably"), in their own words. Empty if they said nothing like that.
- mode: "buy", unless they explicitly want broken items to repair; then "repair".
- summary: one sentence restating what they asked for, in their language. Add nothing they did not say.`

	// maxItems and maxLength keep a model that starts repeating itself from filling
	// the whole answer.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item":          map[string]any{"type": "string", "maxLength": 60},
			"names":         stringList(1, 8),
			"max_price_eur": map[string]any{"type": "number"},
			"radius_m":      map[string]any{"type": "integer"},
			"place":         map[string]any{"type": "string", "maxLength": 60},
			"postcode":      map[string]any{"type": "string"},
			"must_have":     stringList(0, 6),
			"nice_to_have":  stringList(0, 6),
			"mode":          map[string]any{"type": "string", "enum": []string{ModeBuy, ModeRepair}},
			"summary":       map[string]any{"type": "string", "maxLength": 300},
		},
		"required": []string{"item", "names", "max_price_eur", "radius_m", "place", "postcode", "must_have", "nice_to_have", "mode", "summary"},
	}
	var in Intent
	if err := m.Chat(ctx, model, system, description, schema, &in, opt); err != nil {
		// A model that loops once often answers fine the second time.
		in = Intent{}
		if err := m.Chat(ctx, model, system, description, schema, &in, opt); err != nil {
			return Intent{}, err
		}
	}
	// Qwen models sometimes switch to Chinese mid-answer; one retry with an explicit
	// reminder usually fixes it.
	if foreignScript(in) {
		reminder := description + "\n\n(Answer in Dutch, English or French only, using the Latin alphabet.)"
		if err := m.Chat(ctx, model, system, reminder, schema, &in, opt); err != nil {
			return Intent{}, err
		}
		if foreignScript(in) {
			return Intent{}, errors.New("the model answered in another language; try describing it in Dutch")
		}
	}
	tidyIntent(&in, description)
	return in, nil
}

func stringList(minItems, maxItems int) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 80}, "minItems": minItems, "maxItems": maxItems}
}

// foreignScript reports whether any string in v contains letters outside the Latin
// alphabet.
func foreignScript(v any) bool {
	data, _ := json.Marshal(v)
	return strings.IndexFunc(string(data), func(r rune) bool {
		return unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r)
	}) >= 0
}

// genericExclude are phrases found in ordinary for-sale listings; as a name or an
// exclude word they would match nearly everything.
var genericExclude = []string{"te koop", "te verkopen", "verkoop", "verkopen", "aangeboden", "tweedehands",
	"2dehands", "gebruikt", "occasie", "occasion", "nieuw", "goede staat", "prijs"}

// defaultExclude drops rental offers, which share every word with a sale.
var defaultExclude = []string{"te huur", "verhuur"}

var (
	priceRE  = regexp.MustCompile(`(?i)\s*(?:tot|max\.?|maximaal|onder)?\s*€\s*\d+(?:[.,]\d+)?|\s*(?:tot|max\.?|maximaal|onder)?\s*\d+(?:[.,]\d+)?\s*(?:euro|eur|€)`)
	repairRE = regexp.MustCompile(`(?i)defect|kapot|stuk\b|herstel|repar|opknap|broken|repair|fix`)
	partsRE  = regexp.MustCompile(`(?i)onderde|wisselstuk|parts?\b|pièces?|accessoire`)
)

var (
	numberRE = regexp.MustCompile(`\d`)
	radiusRE = regexp.MustCompile(`(?i)\d+\s*(?:km|kilometer)`)
)

// tidyIntent corrects the mistakes small models make: prices or generic words as
// names, the item's own name as a requirement, repair mode for someone who just
// wants to buy, and anything the buyer did not say (a budget, a place, a wish).
func tidyIntent(in *Intent, description string) {
	if !numberRE.MatchString(description) {
		in.MaxPrice = 0
	}
	if !radiusRE.MatchString(description) {
		in.RadiusM = 0
	}
	if in.Place = strings.TrimSpace(in.Place); in.Place == "" || !strings.Contains(strings.ToLower(description), strings.ToLower(in.Place)) {
		in.Place, in.Postcode = "", ""
	}
	if in.Mode != ModeBuy && in.Mode != ModeRepair || in.Mode == ModeRepair && !repairRE.MatchString(description) {
		in.Mode = ModeBuy
	}
	in.Item = strings.TrimSpace(priceRE.ReplaceAllString(in.Item, " "))
	seen := map[string]bool{}
	var names []string
	for _, n := range append([]string{in.Item}, in.Names...) {
		n = strings.ToLower(strings.Join(strings.Fields(priceRE.ReplaceAllString(n, " ")), " "))
		if n == "" || seen[n] || len(strings.Fields(n)) > 2 || inAny(n, genericExclude) {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) > 8 {
		names = names[:8]
	}
	in.Names = names
	in.MustHave = requirements(in.MustHave, names, description)
	in.NiceToHave = requirements(in.NiceToHave, names, description)
	in.Postcode = ValidPostcode(in.Postcode)
}

// requirements keeps the requirements that come from the description: at least
// one of their words must be in it.
func requirements(list, names []string, description string) []string {
	var out []string
	for _, r := range list {
		r = strings.TrimSpace(r)
		if r == "" || priceRE.MatchString(r) || inAny(r, names) || !sharesWord(r, description) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// requestWords are words that only shape the request ("ik zoek ... rond ...").
var requestWords = map[string]bool{
	"zoek": true, "zoeken": true, "gezocht": true, "mijn": true, "naar": true, "graag": true, "liefst": true,
	"bij": true, "voorkeur": true, "minstens": true, "maximum": true, "maximaal": true, "binnen": true,
	"rond": true, "buurt": true, "euro": true, "onder": true, "ongeveer": true, "kilometer": true, "looking": true,
	"want": true, "need": true, "around": true, "within": true, "near": true, "budget": true, "preferably": true,
	"tweedehands": true, "gebruikte": true, "goedkope": true, "goedkoop": true, "moet": true, "hebben": true,
}

// openWords returns the words of the description that nothing in the intent
// accounts for: not the item or its names, the place, a number or a requirement.
// They are often a property the model forgot ("geremde").
func openWords(description string, in Intent) []string {
	names := singleWords(append([]string{in.Item}, in.Names...))
	covered := strings.Join(append(append([]string{in.Place}, in.MustHave...), in.NiceToHave...), " ")
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(priceRE.ReplaceAllString(description, " ")), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len([]rune(w)) < 4 || stopwords[w] || requestWords[w] || inAny(w, out) ||
			prefilter.HasTerm(covered, w) || prefilter.HasAnyCompoundTerm(w, names) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// singleWords keeps the one-word names; longer ones may carry a property
// ("gesloten aanhangwagen") that must not count as the item itself.
func singleWords(names []string) []string {
	var out []string
	for _, n := range names {
		if n != "" && !strings.Contains(n, " ") {
			out = append(out, n)
		}
	}
	return out
}

func sharesWord(phrase, text string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(phrase), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if (len(w) >= 2 || unicode.IsDigit(rune(w[0]))) && !stopwords[w] && prefilter.HasTerm(text, w) {
			return true
		}
	}
	return false
}

func inAny(term string, phrases []string) bool {
	for _, p := range phrases {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(term)) {
			return true
		}
	}
	return false
}

var postcodeRE = regexp.MustCompile(`^[1-9][0-9]{3}(?:\s?[A-Za-z]{2})?$`)

// ValidPostcode keeps a Belgian (1000) or Dutch (1011 or 1011 AB) postcode.
func ValidPostcode(s string) string {
	s = strings.TrimSpace(s)
	if !postcodeRE.MatchString(s) {
		return ""
	}
	return s
}

// Draft is a watch built from the interview, before the user confirms it.
type Draft struct {
	Watch Watch
	// Restated is the model's one-sentence summary of the request.
	Restated string
	// Where describes the subcategories searched, for display.
	Where string
	Notes []string
	// Plan is what the marketplace showed; nil when it had nothing.
	Plan *Plan
}

// Summary renders a draft the way it is shown to the user for confirmation.
func (d Draft) Summary() string {
	w := d.Watch
	var b strings.Builder
	if d.Restated != "" {
		fmt.Fprintf(&b, "%s\n\n", d.Restated)
	}
	switch {
	case w.BrowseSubcategories:
		fmt.Fprintf(&b, "Searches: every listing in %s\n", d.Where)
	case w.OnlySubcategories:
		fmt.Fprintf(&b, "Searches: %s, in %s\n", strings.Join(w.Queries, ", "), d.Where)
	default:
		fmt.Fprintf(&b, "Searches: %s\n", strings.Join(w.Queries, ", "))
	}
	if d.Plan != nil && len(d.Plan.Names) > 1 {
		fmt.Fprintf(&b, "Sellers call it: %s\n", strings.Join(d.Plan.Names, ", "))
	}
	if w.MaxPrice > 0 {
		fmt.Fprintf(&b, "Max price: €%.0f\n", w.MaxPrice)
	}
	if w.RadiusM > 0 || w.Postcode != "" {
		where := "your postcode"
		if w.Postcode != "" {
			where = "postcode " + w.Postcode
		}
		radius := "your usual radius"
		if w.RadiusM > 0 {
			radius = fmt.Sprintf("%d km", w.RadiusM/1000)
		}
		fmt.Fprintf(&b, "Around: %s, %s\n", where, radius)
	}
	if len(w.MustHave) > 0 {
		fmt.Fprintf(&b, "Must have: %s\n", strings.Join(w.MustHave, ", "))
	}
	if len(w.NiceToHave) > 0 {
		fmt.Fprintf(&b, "Nice to have: %s\n", strings.Join(w.NiceToHave, ", "))
	}
	for _, c := range w.Checks {
		fmt.Fprintf(&b, "%q is checked as: %s\n", c.Wish, c.Title())
	}
	if len(w.Exclude) > 0 {
		fmt.Fprintf(&b, "Ignore: %s\n", strings.Join(w.Exclude, ", "))
	}
	for _, note := range d.Notes {
		fmt.Fprintf(&b, "Note: %s\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// SuggestName derives a short watch name from the first phrase.
func SuggestName(phrases []string) string {
	if len(phrases) == 0 {
		return "watch"
	}
	words := strings.Fields(strings.ToLower(phrases[0]))
	if len(words) > 2 {
		words = words[:2]
	}
	name := strings.Join(words, "-")
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "watch"
	}
	return b.String()
}

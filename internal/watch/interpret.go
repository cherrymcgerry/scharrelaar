package watch

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cherrymcgerry/scharrelaar/internal/llm"
	"github.com/cherrymcgerry/scharrelaar/internal/market"
)

// Usage finds what sellers write around the words of a wish, in the plan's
// subcategories nationwide. Small models often do not know what an indirect wish
// implies ("rood-zwarte draad" on speakers: clamp terminals, so passive); the ads
// that use the same words usually say it.
func (x Explorer) Usage(ctx context.Context, plan *Plan, wish string) []string {
	words := wishWords(wish)
	if plan == nil || len(words) == 0 {
		return nil
	}
	seen := map[string]bool{}
	type hit struct {
		text    string
		matched int
	}
	var hits []hit
	for _, query := range wordSets(words) {
		page, err := x.page(ctx, market.Query{L1Category: plan.L1.ID, L2Categories: plan.ids(), Keyword: strings.Join(query, " "), Limit: 30})
		if err != nil {
			break
		}
		for _, l := range page.Listings {
			if seen[l.ItemID] {
				continue
			}
			seen[l.ItemID] = true
			text := l.Title + ". " + l.Description
			if snippet, n := around(text, words); n > 0 {
				hits = append(hits, hit{snippet, n})
			}
		}
		if len(hits) >= 6 {
			break
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].matched > hits[j].matched })
	var out []string
	for _, h := range hits {
		if len(out) == 6 {
			break
		}
		out = append(out, h.text)
	}
	return out
}

// wishWords are the words of a wish worth searching for.
func wishWords(wish string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(wish), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 3 && !stopwords[w] && !requestWords[w] && !inAny(w, out) {
			out = append(out, w)
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// wordSets lists the searches to try: all words, then each set with one word
// left out, so a wish the site has no exact match for still finds related ads.
func wordSets(words []string) [][]string {
	sets := [][]string{words}
	if len(words) > 1 {
		for i := len(words) - 1; i >= 0; i-- {
			set := append(append([]string{}, words[:i]...), words[i+1:]...)
			sets = append(sets, set)
		}
	}
	if len(sets) > 4 {
		sets = sets[:4]
	}
	return sets
}

// around returns the part of text near the first wish word it contains, and how
// many of the words it contains. A word matches on its stem, so "zwart" finds
// "zwarte".
func around(text string, words []string) (string, int) {
	lower := strings.ToLower(text)
	first, matched := -1, 0
	for _, w := range words {
		stem := w
		if r := []rune(w); len(r) > 5 {
			stem = string(r[:len(r)-1])
		}
		if i := strings.Index(lower, stem); i >= 0 {
			matched++
			if first < 0 || i < first {
				first = i
			}
		}
	}
	if matched == 0 {
		return "", 0
	}
	start, end := max(0, first-120), min(len(text), first+160)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	snippet := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet += "..."
	}
	return snippet, matched
}

const indirect = `Buyers often describe what they need indirectly: by a part, a colour, a use, or what they will connect to it. The buyer is NOT buying the thing named in a wish separately; the wish says which KIND of item they need. Sellers almost never use the buyer's words; they write the type, the brand and model, or the technical term.`

// reasonSystem names the item itself: asked about "the item", small models read
// "met rood-zwarte draad" as a cable to buy; asked which kind of luidsprekers it
// describes, they answer passive ones.
const reasonSystem = `You help a buyer on a classifieds site. The buyer wants to buy: %[1]s. They added a wish that describes the %[1]s indirectly, by something they will connect to it, a part, a colour or a use.
The buyer is NOT buying the thing named in the wish separately; the wish says which KIND of %[1]s they need.
Answer in English in short lines:
1. Which part of the %[1]s, or which thing used with it, the wish refers to.
2. Which kind of %[1]s has or needs that: name the technical type, such as active or passive, a connection standard, a size or a fit.
3. The kind of %[1]s to look for, in a few words.
4. The other kinds of %[1]s, which do not fit, and the words or features in an ad that show one of those (built-in parts, wireless, power, a different connection).
5. Only if the wish could reasonably mean something else: lines 1 to 4 for that.
Where ads that use the buyer's words are given, they show what those words mean on this site: rely on them.`

const checkSystem = `You help a buyer on the Belgian and Dutch classifieds sites 2dehands.be and marktplaats.nl.
` + indirect + `
For each numbered wish, give one reading per kind of item its analysis says to look for (line 3, and line 5 when there is one), in that order; without an analysis, up to three readings, the most likely first. For each reading:
- kind: that kind of item, translated to Dutch with all its words (keep words like passive, active, analogue), as a seller would name it. Always a kind of the item itself, never a separate part, cable or accessory.
- means: the property that makes it that kind, in one short Dutch sentence.
- photo: what a photo of a matching item would show, in one English sentence; "" when a photo cannot show it.
- ruled_out_by: up to 8 short words or phrases (Dutch, English or French) that show an item is NOT that kind: the names of the other kinds, and the features only those have (from line 4 of the analysis).
- shown_by: up to 8 short words or phrases a seller could write that show it IS that kind, starting with its name.
Example readings, for a television and the wish "met de rode, gele en witte stekkers":
1. kind "tv met tulp-ingang", means "heeft een composiet (tulp/AV) video-ingang", photo "red, yellow and white round sockets on the back or side", ruled_out_by ["alleen HDMI", "monitor"], shown_by ["AV-ingang", "composiet", "tulp", "cinch", "crt"].
2. kind "tv met scart", means "heeft een SCART-aansluiting", photo "a wide flat 21-pin socket on the back", ruled_out_by ["alleen HDMI", "monitor"], shown_by ["SCART", "scart-ingang", "euroconnector"].
Use words that fit the buyer's item, not the words of this example.
Do not add wishes of your own.`

// Readings are the possible meanings of one wish, the most likely first.
type Readings struct {
	Wish    string
	Options []Check
}

// Interpret works out what each wish could mean for the item and how an ad would
// show it, so a listing is judged on what the buyer needs rather than on whether
// the seller used the buyer's words. usage holds, per wish, what sellers write
// around the same words; it may be empty. Wishes the model could not read are
// left out.
func Interpret(ctx context.Context, m *llm.Manager, model, description, item string, wishes []string, usage map[string][]string, opt llm.Options) ([]Readings, error) {
	if len(wishes) == 0 {
		return nil, nil
	}
	// Models that think before answering spend part of NumPredict on it.
	opt.NumPredict = max(opt.NumPredict, 3000)
	opt.NoThink = false
	// The reasoning is asked for in prose first, one wish at a time: inside a
	// JSON schema, small models mostly restate the wish literally.
	var list strings.Builder
	for i, w := range wishes {
		fmt.Fprintf(&list, "%d. %s\n", i+1, w)
		var ads strings.Builder
		for _, a := range usage[w] {
			fmt.Fprintf(&ads, "- %q\n", a)
		}
		ask := fmt.Sprintf("The buyer wrote: %s\nItem: %s\nWish: %s\n", description, item, w)
		if ads.Len() > 0 {
			ask += "Ads on the site that use these words:\n" + ads.String()
		}
		if analysis, err := m.Ask(ctx, model, fmt.Sprintf(reasonSystem, item), ask, opt); err == nil && strings.TrimSpace(analysis) != "" {
			fmt.Fprintf(&list, "   Analysis:\n%s\n", indent(strings.TrimSpace(analysis), "   "))
		}
	}
	user := fmt.Sprintf("The buyer wrote: %s\nThe item: %s\nWishes:\n%s", description, item, list.String())

	// The model writes the keys in sorted order: the kind first, the words last.
	words := map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 40}, "maxItems": 8}
	reading := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":         map[string]any{"type": "string", "maxLength": 80},
			"means":        map[string]any{"type": "string", "maxLength": 200},
			"photo":        map[string]any{"type": "string", "maxLength": 200},
			"ruled_out_by": words,
			"shown_by":     words,
		},
		"required": []string{"kind", "means", "photo", "ruled_out_by", "shown_by"},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"wishes": map[string]any{
				"type":     "array",
				"minItems": len(wishes),
				"maxItems": len(wishes),
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number":   map[string]any{"type": "integer"},
						"readings": map[string]any{"type": "array", "items": reading, "minItems": 1, "maxItems": 3},
					},
					"required": []string{"number", "readings"},
				},
			},
		},
		"required": []string{"wishes"},
	}
	var answer struct {
		Wishes []struct {
			Number   int `json:"number"`
			Readings []struct {
				Kind    string   `json:"kind"`
				Means   string   `json:"means"`
				Photo   string   `json:"photo"`
				Against []string `json:"ruled_out_by"`
				Signs   []string `json:"shown_by"`
			} `json:"readings"`
		} `json:"wishes"`
	}
	// The thinking was done in the analysis; thinking again here only risks
	// running out of tokens.
	opt.NoThink = true
	if err := m.Chat(ctx, model, checkSystem, user, schema, &answer, opt); err != nil {
		return nil, err
	}
	if foreignScript(answer) {
		return nil, fmt.Errorf("the model answered in another language")
	}
	out := make([]Readings, len(wishes))
	for i, w := range wishes {
		out[i].Wish = w
	}
	for i, a := range answer.Wishes {
		idx := a.Number - 1
		if idx < 0 || idx >= len(wishes) {
			idx = i
		}
		if idx >= len(wishes) || len(out[idx].Options) > 0 {
			continue
		}
		var kinds [][]string
		for _, r := range a.Readings {
			means := strings.TrimSpace(r.Means)
			kind := wishWords(r.Kind)
			if len(kind) == 0 {
				kind = wishWords(means)
			}
			if means == "" || slices.ContainsFunc(kinds, func(k []string) bool { return variant(k, kind) }) {
				continue
			}
			kinds = append(kinds, kind)
			signs := tidyWords(r.Signs)
			out[idx].Options = append(out[idx].Options, Check{
				Wish: wishes[idx], Kind: strings.TrimSpace(r.Kind), Means: means, Signs: signs, Against: ruleOut(r.Against, signs, r.Kind, item),
				Photo: strings.TrimSpace(r.Photo),
			})
		}
	}
	var kept []Readings
	for _, r := range out {
		if len(r.Options) > 0 {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// variant reports whether one reading's words all appear in the other's, as in
// "passieve luidsprekers met klemmen" and "passieve luidsprekers met standaard klemmen".
func variant(a, b []string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for _, w := range a {
		if !slices.Contains(b, w) {
			return false
		}
	}
	return true
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func tidyWords(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range list {
		w = strings.TrimSpace(w)
		key := strings.ToLower(w)
		if w == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, w)
	}
	return out
}

// ruleOut drops the phrases that cannot rule anything out: those naming the item
// itself ("luidspreker" for speakers), and those whose every word is in the kind
// or a sign ("passief" against "passieve luidsprekers").
func ruleOut(against, signs []string, kind, item string) []string {
	itemStems := stems(item)
	wanted := stems(kind + " " + strings.Join(signs, " "))
	var out []string
	for _, w := range tidyWords(against) {
		words := stems(w)
		if len(words) == 0 || len(words) == 1 && slices.Contains(itemStems, words[0]) {
			continue
		}
		if !slices.ContainsFunc(words, func(s string) bool { return !slices.Contains(wanted, s) }) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// stems are the words of s cut to a common root, so "passief", "passieve" and
// "luidspreker", "luidsprekers" match.
func stems(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if r := []rune(w); len(r) > 6 {
			w = string(r[:6])
		}
		out = append(out, w)
	}
	return out
}

// Title names what a check looks for, in one line.
func (c Check) Title() string {
	if c.Kind == "" {
		return c.Means
	}
	return c.Kind + " (" + c.Means + ")"
}

// Describe renders a check for the buyer to confirm.
func (c Check) Describe() string {
	var b strings.Builder
	b.WriteString(c.Title())
	if len(c.Signs) > 0 {
		fmt.Fprintf(&b, "\nAds that say: %s", strings.Join(c.Signs, ", "))
	}
	if len(c.Against) > 0 {
		fmt.Fprintf(&b, "\nNot: %s", strings.Join(c.Against, ", "))
	}
	if c.Photo != "" {
		fmt.Fprintf(&b, "\nOn the photos: %s", c.Photo)
	}
	return b.String()
}

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/cherrymcgerry/scharrelaar/internal/market"
	"github.com/cherrymcgerry/scharrelaar/internal/websearch"
)

const (
	// lookupMaxAge is how long a web lookup of a model is reused.
	lookupMaxAge = 90 * 24 * time.Hour
	// lookupResults is how many search results the judge reads.
	lookupResults = 5
)

// identify returns the brand and model an ad names, as the ad writes them, or
// "" when it names no model.
func (e *Engine) identify(ctx context.Context, l market.Listing) (string, error) {
	text := l.Title + "\n" + clip(l.Description, 1500)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"brand": map[string]any{"type": "string", "maxLength": 40},
			"model": map[string]any{"type": "string", "maxLength": 40},
		},
		"required": []string{"brand", "model"},
	}
	var answer struct {
		Brand string `json:"brand"`
		Model string `json:"model"`
	}
	opt := e.Options
	opt.NoThink = true
	opt.NumPredict = 100
	system := `Copy the brand and the model name or number of the item for sale from the ad, exactly as written. A model is a product name or type number such as "iQ3", "S-3EX" or "SL-1200", not a description such as "speakers" or "vintage". Use "" for what the ad does not name.`
	if err := e.LLM.Chat(ctx, e.Config.Model, system, text, schema, &answer, opt); err != nil {
		return "", err
	}
	brand, model := strings.TrimSpace(answer.Brand), strings.TrimSpace(answer.Model)
	// Both must be the ad's own words, and the model must look like one: a
	// guessed model would make the lookup describe another item.
	if brand == "" || model == "" || !hasDigit(model) && len([]rune(model)) < 3 ||
		!contains(text, brand) || !contains(text, model) {
		return "", nil
	}
	if contains(model, brand) {
		return model, nil
	}
	return brand + " " + model, nil
}

// lookup finds what the web says about the brand and model an ad names. It
// returns the model and the results as text for the judge, or "" when the ad
// names no model or nothing was found.
func (e *Engine) lookup(ctx context.Context, l market.Listing) (model, found string) {
	model, err := e.identify(ctx, l)
	if err != nil || model == "" {
		return "", ""
	}
	query := model + " specifications"
	var results []websearch.Result
	if cached, ok, _ := e.Store.Lookup(strings.ToLower(query), lookupMaxAge); ok {
		json.Unmarshal([]byte(cached), &results)
	} else {
		results, err = e.Web.Search(ctx, query, 10)
		if errors.Is(err, websearch.ErrBlocked) {
			log.Printf("web lookup: %v; not looking anything up until the next scan", err)
			e.webOn = false
			return "", ""
		}
		if err != nil {
			log.Printf("web lookup %q: %v", query, err)
			return "", ""
		}
		raw, _ := json.Marshal(results)
		if err := e.Store.SaveLookup(strings.ToLower(query), string(raw)); err != nil {
			log.Printf("web lookup cache: %v", err)
		}
	}
	// Results that do not name the model are usually about another one.
	var b strings.Builder
	n := 0
	words := strings.Fields(model)
	key := modelKey(words[len(words)-1])
	for _, r := range results {
		if n == lookupResults {
			break
		}
		if !strings.Contains(modelKey(r.Title+" "+r.Snippet), key) {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", r.Title, clip(r.Snippet, 400))
		n++
	}
	if n == 0 {
		return model, ""
	}
	return model, strings.TrimSpace(b.String())
}

// modelKey keeps only the letters and digits, so "S-3EX" matches "S3EX" and
// "S-3EX-T".
func modelKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func contains(text, part string) bool {
	return strings.Contains(squash(text), squash(part))
}

func squash(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func hasDigit(s string) bool {
	return strings.IndexFunc(s, unicode.IsDigit) >= 0
}

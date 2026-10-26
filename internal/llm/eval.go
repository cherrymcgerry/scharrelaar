package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/score"
)

// Options control one evaluation.
type Options struct {
	NumCtx     int
	NumPredict int
	Timeout    time.Duration
	// NoThink asks a thinking model to answer straight away.
	NoThink bool
}

func (o Options) timeout() time.Duration {
	if o.Timeout == 0 {
		return 180 * time.Second
	}
	return o.Timeout
}

// Chat sends one prompt and decodes the model's JSON answer into out, which must be
// a pointer. schema is the JSON schema the model is constrained to.
func (m *Manager) Chat(ctx context.Context, model, system, user string, schema any, out any, opt Options) error {
	return m.ChatImages(ctx, model, system, user, nil, schema, out, opt)
}

// ChatImages is Chat with photos attached to the user message, for a model that
// can see. Images are JPEG or PNG bytes.
func (m *Manager) ChatImages(ctx context.Context, model, system, user string, images [][]byte, schema any, out any, opt Options) error {
	content, err := m.send(ctx, model, system, user, images, schema, opt)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("model returned invalid JSON: %w", err)
	}
	return nil
}

// Ask returns the model's answer as plain text. Small models reason better in
// prose than inside a JSON schema, so a hard question is asked this way first.
func (m *Manager) Ask(ctx context.Context, model, system, user string, opt Options) (string, error) {
	return m.send(ctx, model, system, user, nil, nil, opt)
}

func (m *Manager) send(ctx context.Context, model, system, user string, images [][]byte, schema any, opt Options) (string, error) {
	userMsg := map[string]any{"role": "user", "content": user}
	if len(images) > 0 {
		encoded := make([]string, len(images))
		for i, img := range images {
			encoded[i] = base64.StdEncoding.EncodeToString(img)
		}
		userMsg["images"] = encoded
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			userMsg,
		},
		"stream": false,
		"options": map[string]any{
			"temperature": 0,
			"num_ctx":     opt.NumCtx,
			"num_predict": opt.NumPredict,
		},
	}
	if schema != nil {
		body["format"] = schema
	}
	if opt.NoThink {
		body["think"] = false
	}
	payload, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, opt.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Host()+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		DoneReason string `json:"done_reason"`
		Error      string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding model response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("model error: %s", result.Error)
	}
	if result.DoneReason == "length" {
		return "", fmt.Errorf("the model's answer was cut off after %d tokens (it may be repeating itself)", opt.NumPredict)
	}
	return result.Message.Content, nil
}

// Evaluate scores one listing for repair. The system prompt carries the repair
// knowledge; grounding is applied to the result.
func (m *Manager) Evaluate(ctx context.Context, model, system, listingText string, opt Options) (score.Eval, error) {
	var ev score.Eval
	if err := m.Chat(ctx, model, system, listingText, repairSchema(), &ev, opt); err != nil {
		return ev, err
	}
	score.GroundRepair(&ev, listingText)
	return ev, nil
}

// SelfTest checks that the model returns valid structured output and reports how
// fast it ran.
func (m *Manager) SelfTest(ctx context.Context, model string) (time.Duration, error) {
	start := time.Now()
	var out struct {
		OK bool `json:"ok"`
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean"},
		},
		"required": []string{"ok"},
	}
	err := m.Chat(ctx, model,
		"Answer with JSON only.",
		"Reply with ok set to true.",
		schema, &out, Options{NumCtx: 512, NumPredict: 50, Timeout: 5 * time.Minute, NoThink: true})
	if err != nil {
		return 0, err
	}
	if !out.OK {
		return 0, fmt.Errorf("model answered ok=false")
	}
	return time.Since(start), nil
}

func repairSchema() map[string]any {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc, "maxLength": 400}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"brand_model":                 str("Identified brand and model, or 'unknown'"),
			"device_type":                 str("e.g. amplifier, turntable, 55 inch LED TV, laptop"),
			"is_quality_device":           map[string]any{"type": "boolean"},
			"has_fault":                   map[string]any{"type": "boolean"},
			"fault_quote":                 str("The seller's exact words about the fault, copied verbatim. Empty if none."),
			"fault_is_specific":           map[string]any{"type": "boolean"},
			"fault_category":              map[string]any{"type": "string", "enum": []string{"POWER", "BACKLIGHT", "CHARGING", "SCREEN_PANEL", "LIQUID_DAMAGE", "PHYSICAL_DAMAGE", "BOOT_OR_SOFTWARE", "AUDIO_FAULT", "OTHER", "UNSPECIFIED"}},
			"perceived_issue":             str("Short English summary of the fault"),
			"likely_cause":                str("Most likely technical cause, or 'unknown'"),
			"repair_difficulty":           map[string]any{"type": "string", "enum": []string{"EASY", "MEDIUM", "HARD", "AVOID"}},
			"estimated_parts_cost_eur":    map[string]any{"type": "number"},
			"estimated_working_value_eur": map[string]any{"type": "number"},
			"confidence":                  map[string]any{"type": "string", "enum": []string{"LOW", "MEDIUM", "HIGH"}},
			"red_flags":                   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 5},
			"learning_value":              map[string]any{"type": "string", "enum": []string{"LOW", "MEDIUM", "HIGH"}},
			"skill_learned":               str("Short phrase naming the repair skill this teaches"),
			"reasoning":                   str("Max 2 sentences"),
		},
		"required": []string{"brand_model", "device_type", "is_quality_device", "has_fault", "fault_quote",
			"fault_is_specific", "fault_category", "perceived_issue", "likely_cause", "repair_difficulty",
			"estimated_parts_cost_eur", "estimated_working_value_eur", "confidence", "red_flags",
			"learning_value", "skill_learned", "reasoning"},
	}
}

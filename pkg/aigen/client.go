// Package aigen is the AI schema-generation layer: it turns a natural-language
// description into a VALID Appitools schema. The architecture (AI-F1-S1, research-
// validated) is constrained decoding + a semantic loop:
//
//   - STRUCTURE is constrained at decoding time via Anthropic structured outputs
//     (output_config.format, the strict-grammar mode). For the Appitools schema —
//     an arbitrary-keyed map of resources/fields — the strict subset can only
//     constrain the ENVELOPE (well-formed JSON, the top-level skeleton, the
//     $schema/version consts, no unknown top-level keys); the deep, map-keyed
//     structure (field types, strict keys) is not expressible in the subset (it
//     forbids patternProperties / additionalProperties-as-schema). See OutputSchema.
//   - SEMANTICS (and the deep structure the envelope can't reach) are handled by a
//     short correction loop driven by schema.ValidateReport — the engine's own
//     validator as the trusted external oracle (Huang et al. ICLR 2024: self-
//     correction without an external oracle does not work).
//
// It is a NEW, isolated layer: it imports pkg/schema (the validator) but the
// engine core imports nothing here. The model is reached over raw net/http (the
// standard /v1/messages endpoint) — no new dependency, CGO-free, like OAuth/MFA.
package aigen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Model IDs (verified against the Anthropic model catalog). The thesis under
// test is "a CHEAP model is enough for schema generation", so Haiku is the
// default; the more capable models are here for the economic comparison. The
// model is a pure parameter (the ModelClient seam) — swapping it never touches
// the loop, so the layer is not tied to one model (the SOTA moves constantly).
const (
	ModelHaiku  = "claude-haiku-4-5"  // the cheap model — the thesis
	ModelSonnet = "claude-sonnet-4-6" // mid tier — the comparison
	ModelOpus   = "claude-opus-4-8"   // the most capable — the upper bound
)

// DefaultModel is the model the loop uses unless overridden. Haiku, on purpose:
// the whole point is to show the economy holds with the cheapest model.
const DefaultModel = ModelHaiku

// structuredOutputModels are the models that support Anthropic structured outputs
// (output_config.format). On a model not in this set the generator falls back to
// plain generation (the loop still works, only without the envelope guarantee).
var structuredOutputModels = map[string]bool{
	ModelHaiku:  true,
	ModelSonnet: true,
	ModelOpus:   true,
}

// SupportsStructuredOutput reports whether the model can use structured outputs.
func SupportsStructuredOutput(model string) bool { return structuredOutputModels[model] }

// Pricing is a model's USD price per 1,000,000 tokens (input / output). Cache
// reads are billed at 0.1x input and cache writes at 1.25x input (Anthropic
// prompt-caching economics), applied in Usage.CostUSD.
type Pricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

var modelPricing = map[string]Pricing{
	ModelHaiku:  {InputPerMTok: 1.00, OutputPerMTok: 5.00},
	ModelSonnet: {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	ModelOpus:   {InputPerMTok: 5.00, OutputPerMTok: 25.00},
}

// PricingFor returns the published pricing for a model id (zero value + false
// when the model is unknown).
func PricingFor(model string) (Pricing, bool) {
	p, ok := modelPricing[model]
	return p, ok
}

// Usage is the token accounting for one or more model calls, including the
// prompt-cache split (the stable system prompt is cached, so repeated iterations
// re-read it at 0.1x instead of paying full input each round).
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
}

// Add accumulates another usage into this one (for summing across loop iterations).
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheCreationTokens += o.CacheCreationTokens
	u.CacheReadTokens += o.CacheReadTokens
}

// CostUSD computes the approximate dollar cost of this usage on the given model,
// crediting cache reads (0.1x input) and charging cache writes (1.25x input).
// Returns 0 for an unknown model (the caller can detect that via PricingFor).
func (u Usage) CostUSD(model string) float64 {
	p, ok := modelPricing[model]
	if !ok {
		return 0
	}
	in := p.InputPerMTok
	return (float64(u.InputTokens)*in +
		float64(u.CacheCreationTokens)*in*1.25 +
		float64(u.CacheReadTokens)*in*0.1 +
		float64(u.OutputTokens)*p.OutputPerMTok) / 1e6
}

// Message is one turn in the generate/correct conversation.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Request is one model call. OutputSchema (when non-nil) engages structured
// outputs so the response is decode-constrained to that JSON Schema.
type Request struct {
	System       string
	Messages     []Message
	OutputSchema map[string]any // nil → plain generation (no structured outputs)
}

// Completion is one model response. Refused is set when the model declined the
// request for safety (stop_reason "refusal") — that is NOT a schema error (the
// loop must not try to correct it), it is a model-level decline.
type Completion struct {
	Text    string
	Usage   Usage
	Refused bool
	Refusal string
}

// ModelClient is the seam the loop depends on. The real implementation calls the
// Anthropic API; tests inject a deterministic stub, so the loop is provable with
// NO network and NO API key. The seam is also where a future cheap→expensive
// CASCADE wrapper would live (try Haiku, escalate to Opus on non-convergence) —
// a ModelClient that delegates to others; the loop is unaware.
type ModelClient interface {
	Complete(ctx context.Context, req Request) (Completion, error)
}

// ErrNoAPIKey is returned by NewAnthropicClient when ANTHROPIC_API_KEY is unset,
// so the CLI can print a clear message instead of failing obscurely mid-request.
var ErrNoAPIKey = fmt.Errorf("aigen: ANTHROPIC_API_KEY is not set (export it to use ai-generate; never hardcode it)")

// AnthropicClient is the real ModelClient: a raw net/http call to the standard
// /v1/messages endpoint. No SDK dependency — the request/response shapes are
// stable and small.
type AnthropicClient struct {
	apiKey    string
	model     string
	baseURL   string
	maxTokens int
	http      *http.Client
}

// NewAnthropicClient builds a client for the given model, reading the API key
// from ANTHROPIC_API_KEY (the only supported source — never a literal). The base
// URL can be overridden with ANTHROPIC_BASE_URL (for a proxy/gateway or tests).
// Returns ErrNoAPIKey when the key is absent.
func NewAnthropicClient(model string) (*AnthropicClient, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, ErrNoAPIKey
	}
	if model == "" {
		model = DefaultModel
	}
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return &AnthropicClient{
		apiKey:    key,
		model:     model,
		baseURL:   strings.TrimRight(base, "/"),
		maxTokens: 8192, // a schema is a few KB; 8K output tokens is ample headroom
		http:      &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// Model returns the model id this client targets.
func (c *AnthropicClient) Model() string { return c.model }

type cacheControl struct {
	Type string `json:"type"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type outputFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

type outputConfig struct {
	Format outputFormat `json:"format"`
}

type apiRequest struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       []systemBlock `json:"system,omitempty"`
	Messages     []Message     `json:"messages"`
	OutputConfig *outputConfig `json:"output_config,omitempty"`
}

type apiResponse struct {
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one request and returns the concatenated text content + usage.
// The stable system prompt is sent as a cache_control block (prompt caching), so
// repeated correction rounds re-read it cheaply. A structured request carries
// req.OutputSchema as output_config.format. A safety refusal is surfaced as
// Completion.Refused (not an error) so the loop can stop cleanly.
func (c *AnthropicClient) Complete(ctx context.Context, req Request) (Completion, error) {
	ar := apiRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		Messages:  req.Messages,
	}
	if req.System != "" {
		// One cached block: the system prompt is identical every iteration and
		// across runs, so it is the stable prefix to cache (cache reads ≈ 0.1x).
		ar.System = []systemBlock{{
			Type:         "text",
			Text:         req.System,
			CacheControl: &cacheControl{Type: "ephemeral"},
		}}
	}
	if req.OutputSchema != nil {
		ar.OutputConfig = &outputConfig{Format: outputFormat{Type: "json_schema", Schema: req.OutputSchema}}
	}

	body, err := json.Marshal(ar)
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: call API: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: read response: %w", err)
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Completion{}, fmt.Errorf("aigen: decode response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || parsed.Error != nil {
		msg := fmt.Sprintf("status %d", resp.StatusCode)
		if parsed.Error != nil {
			msg = fmt.Sprintf("%s: %s", parsed.Error.Type, parsed.Error.Message)
		}
		return Completion{}, fmt.Errorf("aigen: API error: %s", msg)
	}

	usage := Usage{
		InputTokens:         parsed.Usage.InputTokens,
		OutputTokens:        parsed.Usage.OutputTokens,
		CacheCreationTokens: parsed.Usage.CacheCreationInputTokens,
		CacheReadTokens:     parsed.Usage.CacheReadInputTokens,
	}

	// Safety refusal: structured outputs' validity guarantee does not apply, and
	// it is not a schema error — surface it so the loop stops cleanly.
	if parsed.StopReason == "refusal" {
		var rb strings.Builder
		for _, b := range parsed.Content {
			if b.Refusal != "" {
				rb.WriteString(b.Refusal)
			} else if b.Type == "text" {
				rb.WriteString(b.Text)
			}
		}
		ref := strings.TrimSpace(rb.String())
		if ref == "" {
			ref = "the model declined this request (safety refusal)"
		}
		return Completion{Usage: usage, Refused: true, Refusal: ref}, nil
	}

	var text strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return Completion{Text: text.String(), Usage: usage}, nil
}

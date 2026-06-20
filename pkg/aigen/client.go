// Package aigen is the AI schema-generation layer (AI-F0-S3): the loop that
// turns a natural-language description into a VALID Appitools schema by
// generating with an LLM, validating against the engine's own validators
// (schema.ValidateReport), and feeding the actionable errors back to the model
// until it converges — the demonstration that the democratization thesis holds:
// the AI produces bounded, verifiable JSON (cheap + tractable), the engine
// guarantees the hard part, and the loop converges without a human.
//
// It is a NEW, isolated layer: it imports pkg/schema (the validator) but the
// engine core (codegen/query/graphql/migration) imports nothing here. The model
// is reached over raw net/http (the standard /v1/messages endpoint) — no new
// dependency, CGO-free, exactly like the OAuth and MFA cores.
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
// default; the more capable models are here for the economic comparison.
const (
	ModelHaiku  = "claude-haiku-4-5"  // the cheap model — the thesis
	ModelSonnet = "claude-sonnet-4-6" // mid tier — the comparison
	ModelOpus   = "claude-opus-4-8"   // the most capable — the upper bound
)

// DefaultModel is the model the loop uses unless overridden. Haiku, on purpose:
// the whole point is to show the economy holds with the cheapest model.
const DefaultModel = ModelHaiku

// Pricing is a model's USD price per 1,000,000 tokens (input / output). Used to
// turn the measured token usage into an approximate cost per generated schema —
// the number that validates (or challenges) the "AI generates schemas cheaply"
// thesis.
type Pricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// modelPricing maps a model id to its published per-MTok price. An unknown model
// yields a zero Pricing (cost reported as 0 with a note), never a panic.
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

// Usage is the token accounting for one or more model calls.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Add accumulates another usage into this one (for summing across loop iterations).
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
}

// CostUSD computes the approximate dollar cost of this usage on the given model.
// Returns 0 for an unknown model (the caller can detect that via PricingFor).
func (u Usage) CostUSD(model string) float64 {
	p, ok := modelPricing[model]
	if !ok {
		return 0
	}
	return float64(u.InputTokens)/1e6*p.InputPerMTok + float64(u.OutputTokens)/1e6*p.OutputPerMTok
}

// Message is one turn in the generate/correct conversation.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Completion is one model response: the text plus its token usage.
type Completion struct {
	Text  string
	Usage Usage
}

// ModelClient is the seam the loop depends on. The real implementation calls the
// Anthropic API; tests inject a deterministic stub, so the loop is provable
// (generate → invalid → correct → valid) with NO network and NO API key.
type ModelClient interface {
	Complete(ctx context.Context, system string, messages []Message) (Completion, error)
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
// URL can be overridden with ANTHROPIC_BASE_URL (for a proxy/gateway). Returns
// ErrNoAPIKey when the key is absent.
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

type apiRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one request and returns the concatenated text content + usage.
func (c *AnthropicClient) Complete(ctx context.Context, system string, messages []Message) (Completion, error) {
	body, err := json.Marshal(apiRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    system,
		Messages:  messages,
	})
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Completion{}, fmt.Errorf("aigen: build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
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

	var text strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return Completion{
		Text: text.String(),
		Usage: Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
		},
	}, nil
}

// Package llm abstracts receipt extraction behind a small provider
// interface. Fireworks (hosting Kimi K2.7) is the default, but its API is
// OpenAI-wire-compatible, so swapping providers/models is an env var change
// (LLM_PROVIDER, LLM_BASE_URL, LLM_MODEL, LLM_API_KEY), not a code change.
package llm

import "context"

type ExtractedItem struct {
	Name       string  `json:"name"`
	PriceCents int64   `json:"price_cents"`
	Quantity   float64 `json:"quantity"`
}

type ExtractedReceipt struct {
	RestaurantName string          `json:"restaurant_name,omitempty"`
	Date           string          `json:"date,omitempty"` // best-effort ISO 8601
	Items          []ExtractedItem `json:"items"`
}

// Usage lets callers (internal/ratelimit's spend cap) charge the actual
// cost of a call rather than only ever guessing from an estimate.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type Result struct {
	Receipt ExtractedReceipt
	Usage   Usage
}

type Provider interface {
	ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*Result, error)
	Name() string
}

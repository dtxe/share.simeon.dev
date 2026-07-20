// Package llm abstracts receipt extraction behind a small provider
// interface. Fireworks (hosting MiniMax M3) is the default, but its API is
// OpenAI-wire-compatible, so swapping providers/models is an env var change
// (LLM_PROVIDER, LLM_BASE_URL, LLM_MODEL, LLM_API_KEY), not a code change.
package llm

import "context"

type ExtractedItem struct {
	Name       string  `json:"name"`
	PriceCents int64   `json:"priceCents"`
	Quantity   float64 `json:"quantity"`
}

type ExtractedReceipt struct {
	RestaurantName string          `json:"restaurantName,omitempty"`
	Date           string          `json:"date,omitempty"` // best-effort ISO 8601
	SubtotalCents  int64           `json:"subtotalCents,omitempty"`
	TipCents       int64           `json:"tipCents,omitempty"`
	TotalPaidCents int64           `json:"totalPaidCents,omitempty"`
	Items          []ExtractedItem `json:"items"`
}

// Usage lets callers (internal/ratelimit's spend cap) charge the actual
// cost of a call rather than only ever guessing from an estimate.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Attempt is one upstream chat-completions response. RawResponse is the
// response body exactly as received (it never contains the request/image).
type Attempt struct {
	RawResponse []byte
	Usage       Usage
}

// ResponseError preserves responses received before a provider-side or
// client-side response validation error.
type ResponseError struct {
	Err      error
	Attempts []Attempt
}

func (e *ResponseError) Error() string { return e.Err.Error() }
func (e *ResponseError) Unwrap() error { return e.Err }

type Result struct {
	Receipt     ExtractedReceipt
	Usage       Usage
	RawResponse []byte
	// Attempts includes every upstream turn made by this operation.
	Attempts []Attempt
}

type Provider interface {
	ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*Result, error)
	Name() string
}

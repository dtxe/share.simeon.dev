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
	Taxable    *bool   `json:"taxable,omitempty"`
}

type ExtractedReceipt struct {
	RestaurantName           string          `json:"restaurantName,omitempty"`
	Date                     string          `json:"date,omitempty"` // best-effort ISO 8601
	SubtotalCents            int64           `json:"subtotalCents,omitempty"`
	TipCents                 int64           `json:"tipCents,omitempty"`
	TotalPaidCents           int64           `json:"totalPaidCents,omitempty"`
	TaxCents                 *int64          `json:"taxCents,omitempty"`
	TaxRateBasisPoints       *int64          `json:"taxRateBasisPoints,omitempty"`
	TipKnown                 *bool           `json:"tipKnown,omitempty"`
	HasNonTaxAdjustments     *bool           `json:"hasNonTaxAdjustments,omitempty"`
	MultipleTaxRatesDetected *bool           `json:"multipleTaxRatesDetected,omitempty"`
	Items                    []ExtractedItem `json:"items"`
}

// NormalizeTaxable applies the deliberately conservative default: an omitted
// taxable marker means taxable. It is called after decoding, before math.
func (r *ExtractedReceipt) NormalizeTaxable() {
	for i := range r.Items {
		if r.Items[i].Taxable == nil {
			v := true
			r.Items[i].Taxable = &v
		}
	}
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

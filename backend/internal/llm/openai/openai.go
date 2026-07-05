// Package openai is a second receipt-extraction provider — it exists
// primarily to prove the provider abstraction actually holds: swapping from
// Fireworks to OpenAI (or any other OpenAI-wire-compatible host) is purely
// an env var change (LLM_PROVIDER=openai, LLM_BASE_URL, LLM_MODEL,
// LLM_API_KEY), reusing the exact same request/response handling.
package openai

import (
	"context"

	"cher-app/backend/internal/llm"
	"cher-app/backend/internal/llm/openaicompat"
)

type Provider struct {
	client *openaicompat.Client
	model  string
}

func New(baseURL, apiKey, model string) *Provider {
	return &Provider{
		client: openaicompat.New(baseURL, apiKey, model),
		model:  model,
	}
}

func (p *Provider) Name() string { return "openai:" + p.model }

func (p *Provider) ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*llm.Result, error) {
	return p.client.ExtractReceipt(ctx, image, mimeType)
}

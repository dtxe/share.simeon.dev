// Package fireworks is the default receipt-extraction provider: Fireworks
// AI's OpenAI-wire-compatible chat completions endpoint, hosting Kimi K2.7.
package fireworks

import (
	"context"

	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
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

func (p *Provider) Name() string { return "fireworks:" + p.model }

func (p *Provider) ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*llm.Result, error) {
	return p.client.ExtractReceipt(ctx, image, mimeType)
}

// Package openaicompat implements receipt extraction against any
// OpenAI-wire-compatible chat completions endpoint (Fireworks, OpenAI
// itself, and most other hosted-inference providers). internal/llm/fireworks
// and internal/llm/openai are both thin wrappers around this Client,
// configured with a different base URL/model/key — proof that switching
// providers doesn't require new request/response handling code.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"share/backend/internal/llm"
)

const extractionPrompt = `You are extracting structured data from a photo of a restaurant receipt.
Return the restaurant name, the date (ISO 8601, best effort), and every line item with its name,
price in integer cents, and quantity. If a field can't be determined, omit it rather than guessing wildly.
Respond using exactly these JSON field names: restaurantName, date, items (each with name, priceCents, quantity).`

type Client struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema jsonSchemaSpec `json:"json_schema"`
}

type jsonSchemaSpec struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	MaxTokens      int            `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

var extractionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"restaurantName": map[string]any{"type": "string"},
		"date":           map[string]any{"type": "string"},
		"items": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":       map[string]any{"type": "string"},
					"priceCents": map[string]any{"type": "integer"},
					"quantity":   map[string]any{"type": "number"},
				},
				"required": []string{"name", "priceCents", "quantity"},
			},
		},
	},
	"required": []string{"items"},
}

func (c *Client) ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*llm.Result, error) {
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(image))

	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{
				Role: "user",
				Content: []contentPart{
					{Type: "text", Text: extractionPrompt},
					{Type: "image_url", ImageURL: &imageURL{URL: dataURL}},
				},
			},
		},
		ResponseFormat: responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaSpec{
				Name:   "receipt_extraction",
				Strict: true,
				Schema: extractionSchema,
			},
		},
		MaxTokens: 2000,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Deliberately don't include respBody in the returned error — it may
		// contain the request echoed back, and callers only ever surface a
		// generic "extraction failed" to the client anyway (see httpapi).
		return nil, fmt.Errorf("openaicompat: upstream status %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding response envelope: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openaicompat: no choices in response")
	}

	var receipt llm.ExtractedReceipt
	dec := json.NewDecoder(bytes.NewReader([]byte(chatResp.Choices[0].Message.Content)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding extracted receipt JSON: %w", err)
	}

	return &llm.Result{
		Receipt: receipt,
		Usage: llm.Usage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
		},
	}, nil
}

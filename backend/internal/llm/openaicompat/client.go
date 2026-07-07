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
	"log"
	"net/http"
	"time"

	"share/backend/internal/llm"
)

const extractionPrompt = `You are extracting structured data from a photo of a restaurant receipt.
Return the restaurant name, the date (ISO 8601, best effort), and every line item with its name,
unit price in integer cents (priceCents is the price for a single item, not the line total), and
quantity. Also return, in integer cents: the pre-tax subtotal (subtotalCents), any tip/gratuity
amount (tipCents), and the final total actually charged including tax and tip (totalPaidCents) —
prefer a credit-card/charged-amount line for totalPaidCents when one is printed. Only include
amounts clearly printed on the receipt; if a field can't be determined, omit it rather than guessing
wildly.
Where a pre-tax subtotal is printed, verify that the sum of each item's priceCents times its
quantity equals subtotalCents. If they don't match, re-read the item lines and the subtotal line and
correct whichever was misread — the most common error is recording the line total (unit price times
quantity) as priceCents instead of the per-item unit price.
Call the extract_receipt function with the result — do not respond in plain text.`

const minimizeReasoningPromptSuffix = `Minimize thinking: use only the reasoning needed to read the receipt, then call the extract_receipt function directly.`

const (
	extractFunctionName             = "extract_receipt"
	extractionMaxTokens             = 4000
	extractionReasoningBudgetTokens = extractionMaxTokens / 2
	kimiK2P7CodeModel               = "accounts/fireworks/models/kimi-k2p7-code"
	minimaxM3Model                  = "accounts/fireworks/models/minimax-m3"
)

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

type toolDef struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type toolChoice struct {
	Type     string           `json:"type"`
	Function toolChoiceTarget `json:"function"`
}

type toolChoiceTarget struct {
	Name string `json:"name"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type chatRequest struct {
	Model      string          `json:"model"`
	Messages   []chatMessage   `json:"messages"`
	Tools      []toolDef       `json:"tools"`
	ToolChoice toolChoice      `json:"tool_choice"`
	Thinking   *thinkingConfig `json:"thinking,omitempty"`
	MaxTokens  int             `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
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
		"subtotalCents":  map[string]any{"type": "integer"},
		"tipCents":       map[string]any{"type": "integer"},
		"totalPaidCents": map[string]any{"type": "integer"},
		"items": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":       map[string]any{"type": "string"},
					"priceCents": map[string]any{"type": "integer", "description": "per-item unit price in cents, not the line total"},
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
	prompt := extractionPromptForModel(c.Model)

	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{
				Role: "user",
				Content: []contentPart{
					{Type: "text", Text: prompt},
					{Type: "image_url", ImageURL: &imageURL{URL: dataURL}},
				},
			},
		},
		Tools: []toolDef{
			{
				Type: "function",
				Function: functionDef{
					Name:       extractFunctionName,
					Parameters: extractionSchema,
				},
			},
		},
		ToolChoice: toolChoice{
			Type:     "function",
			Function: toolChoiceTarget{Name: extractFunctionName},
		},
		Thinking:  thinkingConfigForModel(c.Model),
		MaxTokens: extractionMaxTokens,
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
		// Callers only ever surface a generic "extraction failed" to the
		// client (see httpapi), but a truncated snippet is safe and useful
		// in server-side debug logs — this is the upstream's own error body,
		// not our request (which contains the base64 image and stays out).
		snippet := respBody
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		return nil, fmt.Errorf("openaicompat: upstream status %d: %s", resp.StatusCode, snippet)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding response envelope: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openaicompat: no choices in response")
	}

	if chatResp.Choices[0].FinishReason == "length" {
		log.Printf("llm: extraction truncated at max_tokens (model=%s, completion_tokens=%d, reasoning_budget=%d) — tool-call JSON may be incomplete",
			c.Model, chatResp.Usage.CompletionTokens, extractionReasoningBudgetTokens)
	}

	msg := chatResp.Choices[0].Message
	if len(msg.ToolCalls) == 0 {
		return nil, fmt.Errorf("openaicompat: model returned no tool call (content: %s)", msg.Content)
	}
	call := msg.ToolCalls[0]
	if call.Function.Name != extractFunctionName {
		return nil, fmt.Errorf("openaicompat: unexpected tool call %q", call.Function.Name)
	}

	var receipt llm.ExtractedReceipt
	dec := json.NewDecoder(bytes.NewReader([]byte(call.Function.Arguments)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding extracted receipt tool call arguments: %w (raw arguments: %s)", err, call.Function.Arguments)
	}

	return &llm.Result{
		Receipt: receipt,
		Usage: llm.Usage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
		},
	}, nil
}

func extractionPromptForModel(model string) string {
	if thinkingConfigForModel(model) != nil {
		return extractionPrompt
	}
	return extractionPrompt + "\n\n" + minimizeReasoningPromptSuffix
}

func thinkingConfigForModel(model string) *thinkingConfig {
	switch model {
	case kimiK2P7CodeModel, minimaxM3Model:
		return &thinkingConfig{
			Type:         "enabled",
			BudgetTokens: extractionReasoningBudgetTokens,
		}
	default:
		return nil
	}
}

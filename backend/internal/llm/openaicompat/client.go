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
	"math"
	"net/http"
	"strings"
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
You may use the interim_calculation calculator to verify the subtotal before returning the extraction.
Keep private reasoning terse: use short fragments, no filler or restatement, and state each fact once.
Preserve exact receipt text and numbers. Do not omit an accuracy check to be shorter.
Call the extract_receipt function with the result — do not respond in plain text.`

const minimizeReasoningPromptSuffix = `Minimize thinking: use only the reasoning needed to read the receipt, then call the extract_receipt function directly.`

const (
	extractFunctionName             = "extract_receipt"
	interimCalculationFunctionName  = "interim_calculation"
	promptCacheKey                  = "share-receipt-extraction-v1"
	extractionMaxTokens             = 6000
	extractionReasoningBudgetTokens = 4000
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
	Role       string        `json:"role"`
	Content    []contentPart `json:"content,omitempty"`
	ToolCalls  []toolCallOut `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

// toolCallOut is the assistant-role echo of a prior tool call, sent back as
// conversation history on a follow-up turn (e.g. feedback.Strategy's second
// call) — the wire shape a provider expects when replaying its own earlier
// tool_calls entry.
type toolCallOut struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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
	Required bool             `json:"-"`
}

func (c toolChoice) MarshalJSON() ([]byte, error) {
	if c.Required {
		return json.Marshal("required")
	}
	type alias toolChoice
	return json.Marshal(alias(c))
}

func (c *toolChoice) UnmarshalJSON(data []byte) error {
	if string(data) == `"required"` {
		c.Required = true
		return nil
	}
	type alias toolChoice
	return json.Unmarshal(data, (*alias)(c))
}

type toolChoiceTarget struct {
	Name string `json:"name"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Tools          []toolDef       `json:"tools"`
	ToolChoice     toolChoice      `json:"tool_choice"`
	PromptCacheKey string          `json:"prompt_cache_key"`
	Thinking       *thinkingConfig `json:"thinking,omitempty"`
	MaxTokens      int             `json:"max_tokens"`
}

type chatResponse struct {
	Choices []responseChoice `json:"choices"`
	Usage   responseUsage    `json:"usage"`
}

type responseChoice struct {
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type responseMessage struct {
	Content   string             `json:"content"`
	ToolCalls []responseToolCall `json:"tool_calls"`
}

type responseToolCall struct {
	ID       string           `json:"id"`
	Function toolCallFunction `json:"function"`
}

type responseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
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
	raw, usage, err := c.ExtractWithSchema(ctx, image, mimeType, extractionPromptForModel(c.Model), extractionSchema, extractionReasoningBudgetTokens)
	if err != nil {
		return nil, err
	}
	receipt, err := decodeReceipt(raw)
	if err != nil {
		return nil, err
	}
	return &llm.Result{Receipt: receipt, Usage: usage}, nil
}

// ExtractAttemptResult is one LLM call's outcome, including the tool_call_id
// a follow-up call needs to replay this attempt as real conversation history
// (see ExtractReceiptFeedback).
type ExtractAttemptResult struct {
	ToolCallID   string
	Receipt      llm.ExtractedReceipt
	RawArguments json.RawMessage
	Usage        llm.Usage
}

// ExtractReceiptAttempt is ExtractReceipt plus the tool_call_id and raw
// arguments a strategy needs to set up a feedback retry (feedback.Strategy's
// first call) — same prompt/schema/budget as ExtractReceipt, so behavior on
// this first call is identical to the baseline pipeline.
func (c *Client) ExtractReceiptAttempt(ctx context.Context, image []byte, mimeType string) (*ExtractAttemptResult, error) {
	messages := []chatMessage{buildUserMessage(extractionPromptForModel(c.Model), image, mimeType)}
	toolCallID, raw, usage, err := c.doExtract(ctx, messages, extractionSchema, extractionReasoningBudgetTokens)
	if err != nil {
		return nil, err
	}
	receipt, err := decodeReceipt(raw)
	if err != nil {
		return nil, err
	}
	return &ExtractAttemptResult{ToolCallID: toolCallID, Receipt: receipt, RawArguments: raw, Usage: usage}, nil
}

// ExtractReceiptFeedback is a second look at the same image: it replays the
// prior attempt as real conversation history (the original user message, the
// model's own tool call, an acknowledging tool result) and re-sends the image
// alongside a new user message describing the discrepancy, then asks the
// model to call extract_receipt again. Same schema/budget as ExtractReceipt.
func (c *Client) ExtractReceiptFeedback(ctx context.Context, image []byte, mimeType, priorToolCallID string, priorRawArguments json.RawMessage, feedback string) (*ExtractAttemptResult, error) {
	messages := []chatMessage{
		buildUserMessage(extractionPromptForModel(c.Model), image, mimeType),
		{
			Role: "assistant",
			ToolCalls: []toolCallOut{{
				ID:   priorToolCallID,
				Type: "function",
				Function: toolCallFunction{
					Name:      extractFunctionName,
					Arguments: string(priorRawArguments),
				},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: priorToolCallID,
			Content:    []contentPart{{Type: "text", Text: "received"}},
		},
		buildUserMessage(feedback, image, mimeType),
	}
	toolCallID, raw, usage, err := c.doExtract(ctx, messages, extractionSchema, extractionReasoningBudgetTokens)
	if err != nil {
		return nil, err
	}
	receipt, err := decodeReceipt(raw)
	if err != nil {
		return nil, err
	}
	return &ExtractAttemptResult{ToolCallID: toolCallID, Receipt: receipt, RawArguments: raw, Usage: usage}, nil
}

func decodeReceipt(raw json.RawMessage) (llm.ExtractedReceipt, error) {
	var receipt llm.ExtractedReceipt
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return llm.ExtractedReceipt{}, fmt.Errorf("openaicompat: decoding extracted receipt tool call arguments: %w (raw arguments: %s)", err, raw)
	}
	return receipt, nil
}

func buildUserMessage(text string, image []byte, mimeType string) chatMessage {
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(image))
	return chatMessage{
		Role: "user",
		Content: []contentPart{
			{Type: "text", Text: text},
			{Type: "image_url", ImageURL: &imageURL{URL: dataURL}},
		},
	}
}

// ExtractWithSchema is the shared request/response plumbing (HTTP call,
// base64 image part, forced tool-call decode, finish_reason:"length"
// handling) behind every extraction strategy variant — only the prompt,
// tool schema, and thinking budget differ between them, and each strategy
// decodes the returned raw tool-call arguments into its own schema-specific
// struct. ExtractReceipt is itself just this called with the baseline
// prompt/schema/budget, decoded into llm.ExtractedReceipt.
func (c *Client) ExtractWithSchema(ctx context.Context, image []byte, mimeType, prompt string, schema map[string]any, thinkingBudgetTokens int) (json.RawMessage, llm.Usage, error) {
	_, raw, usage, err := c.doExtract(ctx, []chatMessage{buildUserMessage(prompt, image, mimeType)}, schema, thinkingBudgetTokens)
	return raw, usage, err
}

// doExtract is the shared request/response plumbing (HTTP call, forced
// tool-call decode, finish_reason:"length" handling) behind every extraction
// call, single- or multi-turn — only the messages, schema, and thinking
// budget differ between callers.
func (c *Client) doExtract(ctx context.Context, messages []chatMessage, schema map[string]any, thinkingBudgetTokens int) (toolCallID string, args json.RawMessage, usage llm.Usage, err error) {
	tools := extractionTools(schema)
	first, usage, err := c.chat(ctx, messages, tools, toolChoice{Required: true}, thinkingBudgetTokens)
	if err != nil {
		return "", nil, usage, err
	}
	if len(first.Choices[0].Message.ToolCalls) == 0 {
		return "", nil, usage, fmt.Errorf("openaicompat: model returned no tool call (content: %s)", first.Choices[0].Message.Content)
	}
	call := first.Choices[0].Message.ToolCalls[0]
	if call.Function.Name == extractFunctionName {
		return normalizedToolCallID(call.ID), json.RawMessage(call.Function.Arguments), usage, nil
	}
	if call.Function.Name != interimCalculationFunctionName {
		return "", nil, usage, fmt.Errorf("openaicompat: unexpected tool call %q", call.Function.Name)
	}
	calculation, err := calculateInterim(call.Function.Arguments)
	if err != nil {
		return "", nil, usage, err
	}
	callID := normalizedToolCallID(call.ID)
	messages = append(messages,
		chatMessage{Role: "assistant", ToolCalls: []toolCallOut{{
			ID: callID, Type: "function", Function: call.Function,
		}}},
		chatMessage{Role: "tool", ToolCallID: callID, Content: []contentPart{{Type: "text", Text: calculation}}},
	)
	final, finalUsage, err := c.chat(ctx, messages, tools, toolChoice{Type: "function", Function: toolChoiceTarget{Name: extractFunctionName}}, thinkingBudgetTokens)
	usage.PromptTokens += finalUsage.PromptTokens
	usage.CompletionTokens += finalUsage.CompletionTokens
	if err != nil {
		return "", nil, usage, err
	}
	if len(final.Choices[0].Message.ToolCalls) == 0 {
		return "", nil, usage, fmt.Errorf("openaicompat: model returned no tool call (content: %s)", final.Choices[0].Message.Content)
	}
	finalCall := final.Choices[0].Message.ToolCalls[0]
	if finalCall.Function.Name != extractFunctionName {
		return "", nil, usage, fmt.Errorf("openaicompat: unexpected tool call %q", finalCall.Function.Name)
	}
	return normalizedToolCallID(finalCall.ID), json.RawMessage(finalCall.Function.Arguments), usage, nil
}

func extractionTools(schema map[string]any) []toolDef {
	calculatorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": map[string]any{
				"type":        "array",
				"description": "items to total; always pass an array, including for one item",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"p": map[string]any{"type": "integer", "description": "price in cents"},
						"n": map[string]any{"type": "number", "description": "quantity"},
					},
					"required":             []string{"p", "n"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"item"},
		"additionalProperties": false,
	}
	return []toolDef{
		{Type: "function", Function: functionDef{Name: extractFunctionName, Parameters: schema}},
		{Type: "function", Function: functionDef{
			Name:        interimCalculationFunctionName,
			Description: "Calculate the receipt subtotal from item prices and quantities.",
			Parameters:  calculatorSchema,
		}},
	}
}

func (c *Client) chat(ctx context.Context, messages []chatMessage, tools []toolDef, choice toolChoice, thinkingBudgetTokens int) (chatResponse, llm.Usage, error) {
	reqBody := chatRequest{
		Model:          c.Model,
		Messages:       messages,
		Tools:          tools,
		ToolChoice:     choice,
		PromptCacheKey: promptCacheKey,
		Thinking:       thinkingConfigForModel(c.Model, thinkingBudgetTokens),
		MaxTokens:      extractionMaxTokens,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: reading response: %w", err)
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
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: upstream status %d: %s", resp.StatusCode, snippet)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: decoding response envelope: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return chatResponse{}, llm.Usage{}, fmt.Errorf("openaicompat: no choices in response")
	}

	usage := llm.Usage{
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
	}

	if chatResp.Choices[0].FinishReason == "length" {
		log.Printf("llm: extraction truncated at max_tokens (model=%s, completion_tokens=%d, reasoning_budget=%d) — tool-call JSON may be incomplete",
			c.Model, chatResp.Usage.CompletionTokens, thinkingBudgetTokens)
	}

	return chatResp, usage, nil
}

func normalizedToolCallID(id string) string {
	if id == "" {
		return extractFunctionName + "_call"
	}
	return id
}

type interimCalculationInput struct {
	Item json.RawMessage `json:"item"`
}

type interimCalculationItem struct {
	Price    *int64       `json:"p"`
	Quantity *json.Number `json:"n"`
}

func calculateInterim(raw string) (string, error) {
	var input interimCalculationInput
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return "", fmt.Errorf("openaicompat: invalid interim_calculation arguments: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("openaicompat: invalid interim_calculation arguments: trailing JSON")
		}
		return "", fmt.Errorf("openaicompat: invalid interim_calculation arguments: %w", err)
	}
	items, err := decodeInterimItems(input.Item)
	if err != nil {
		return "", err
	}
	var subtotal int64
	for _, item := range items {
		if item.Price == nil || item.Quantity == nil || *item.Price < 0 {
			return "", fmt.Errorf("openaicompat: invalid interim_calculation item")
		}
		qty, err := item.Quantity.Float64()
		if err != nil || math.IsNaN(qty) || math.IsInf(qty, 0) {
			return "", fmt.Errorf("openaicompat: invalid interim_calculation item")
		}
		if qty <= 0 {
			qty = 1
		}
		value := math.Round(float64(*item.Price) * qty)
		if value < 0 || value >= float64(math.MaxInt64) {
			return "", fmt.Errorf("openaicompat: interim_calculation overflow")
		}
		lineTotal := int64(value)
		if subtotal > math.MaxInt64-lineTotal {
			return "", fmt.Errorf("openaicompat: interim_calculation overflow")
		}
		subtotal += lineTotal
	}
	result, _ := json.Marshal(map[string]int64{"subtotalCents": subtotal})
	return string(result), nil
}

func decodeInterimItems(raw json.RawMessage) ([]interimCalculationItem, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("openaicompat: invalid interim_calculation arguments: missing item")
	}

	if raw[0] == '{' {
		var item interimCalculationItem
		if err := decodeInterimJSON(raw, &item); err != nil {
			return nil, err
		}
		return []interimCalculationItem{item}, nil
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("openaicompat: invalid interim_calculation arguments: item must be an array")
	}

	var items []interimCalculationItem
	if err := decodeInterimJSON(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeInterimJSON(raw json.RawMessage, value any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return fmt.Errorf("openaicompat: invalid interim_calculation arguments: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("openaicompat: invalid interim_calculation arguments: trailing JSON")
		}
		return fmt.Errorf("openaicompat: invalid interim_calculation arguments: %w", err)
	}
	return nil
}

func extractionPromptForModel(model string) string {
	if SupportsThinking(model) {
		return extractionPrompt
	}
	return extractionPrompt + "\n\n" + minimizeReasoningPromptSuffix
}

// SupportsThinking reports whether model is known to accept the
// Fireworks/Anthropic-compatible "thinking" request field — live-verified
// per model, per docs/agent_lessons.md (unverified models risk a 400).
// Exported so other strategies can decide their own prompt/budget choices
// consistently with this gate.
func SupportsThinking(model string) bool {
	switch model {
	case kimiK2P7CodeModel, minimaxM3Model:
		return true
	default:
		return false
	}
}

// MinimizeReasoningPromptSuffix is the prompt-level fallback for models that
// don't support the "thinking" field's hard budget cap.
const MinimizeReasoningPromptSuffix = minimizeReasoningPromptSuffix

// MinThinkingBudgetTokens is Fireworks' live-verified floor for
// thinking.budget_tokens (rejects anything lower with a 400) — confirmed
// against both supported models before this const was added.
const MinThinkingBudgetTokens = 1024

func thinkingConfigForModel(model string, budgetTokens int) *thinkingConfig {
	if !SupportsThinking(model) {
		return nil
	}
	return &thinkingConfig{
		Type:         "enabled",
		BudgetTokens: budgetTokens,
	}
}

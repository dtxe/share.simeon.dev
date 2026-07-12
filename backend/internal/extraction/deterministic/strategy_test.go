package deterministic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"share/backend/internal/llm/openaicompat"
)

const fakeMinimaxModel = "accounts/fireworks/models/minimax-m3"

func TestRunSendsDeterministicPromptSchemaAndLowThinkingBudget(t *testing.T) {
	var gotRaw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRaw); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		args := `{"items":[{"name":"Pad Thai","printedPriceCents":1200,"quantity":1}],"subtotalCents":1200}`
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"tool_calls": []map[string]any{
							{"function": map[string]any{"name": "extract_receipt", "arguments": args}},
						},
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 500, "completion_tokens": 80},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Schema uses printedPriceCents, not priceCents.
	tools, _ := gotRaw["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %+v", gotRaw["tools"])
	}
	schemaJSON, _ := json.Marshal(tools[0])
	if !strings.Contains(string(schemaJSON), "printedPriceCents") {
		t.Fatalf("expected schema to use printedPriceCents, got %s", schemaJSON)
	}
	if strings.Contains(string(schemaJSON), `"priceCents"`) {
		t.Fatalf("schema must not use baseline's priceCents field, got %s", schemaJSON)
	}

	// Prompt forbids self-correction, doesn't ask for reconciliation.
	messages, _ := gotRaw["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", messages)
	}
	msg, _ := messages[0].(map[string]any)
	content, _ := msg["content"].([]any)
	part0, _ := content[0].(map[string]any)
	promptText, _ := part0["text"].(string)
	if !strings.Contains(promptText, "Do not attempt to reconcile") {
		t.Fatalf("expected prompt to forbid self-correction, got: %q", promptText)
	}
	if strings.Contains(promptText, "correct whichever was misread") {
		t.Fatalf("must not reuse baseline's self-reconciliation instruction, got: %q", promptText)
	}

	// Thinking budget is the live-verified Fireworks minimum, not baseline's 2000.
	thinking, ok := gotRaw["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking field for a thinking-capable model, got %+v", gotRaw["thinking"])
	}
	if budget, _ := thinking["budget_tokens"].(float64); int(budget) != openaicompat.MinThinkingBudgetTokens {
		t.Fatalf("thinking.budget_tokens = %v, want %d", thinking["budget_tokens"], openaicompat.MinThinkingBudgetTokens)
	}

	// End-to-end: ResolvePriceFormat ran and produced a matched result.
	if result.SubtotalMatched == nil || !*result.SubtotalMatched {
		t.Fatalf("SubtotalMatched = %v, want true", result.SubtotalMatched)
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].PriceCents != 1200 {
		t.Fatalf("unexpected resolved items: %+v", result.Receipt.Items)
	}
}

func TestRunOmitsThinkingAndAddsMinimizeSuffixForUnknownModel(t *testing.T) {
	var gotRaw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRaw); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		args := `{"items":[]}`
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"tool_calls": []map[string]any{
							{"function": map[string]any{"name": "extract_receipt", "arguments": args}},
						},
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", "accounts/example/models/custom")
	strategy := New(client, "custom:model", "accounts/example/models/custom", 1, 1)

	if _, err := strategy.Run(context.Background(), []byte("x"), "image/jpeg"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, present := gotRaw["thinking"]; present {
		t.Fatalf("expected no thinking field for an unverified model, got %v", gotRaw["thinking"])
	}

	messages, _ := gotRaw["messages"].([]any)
	msg, _ := messages[0].(map[string]any)
	content, _ := msg["content"].([]any)
	part0, _ := content[0].(map[string]any)
	promptText, _ := part0["text"].(string)
	if !strings.Contains(promptText, openaicompat.MinimizeReasoningPromptSuffix) {
		t.Fatalf("expected minimize-reasoning suffix in prompt, got: %q", promptText)
	}
}

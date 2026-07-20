package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractReceiptParsesResponse(t *testing.T) {
	var gotAuth, gotModel string
	var gotBody chatRequest
	var gotRaw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotRaw); err != nil {
			t.Errorf("decoding raw request body: %v", err)
		}
		gotModel = gotBody.Model

		if !gotBody.ToolChoice.Required || len(gotBody.Tools) != 2 || gotBody.Tools[0].Function.Name != extractFunctionName || gotBody.Tools[1].Function.Name != interimCalculationFunctionName {
			t.Errorf("expected required choice with extraction and calculator tools, got choice=%+v tools=%+v", gotBody.ToolChoice, gotBody.Tools)
		}
		calculatorSchema := gotBody.Tools[1].Function.Parameters
		required, ok := calculatorSchema["required"].([]any)
		if !ok || len(required) != 1 || required[0] != "item" {
			t.Errorf("calculator required fields = %#v, want item", calculatorSchema["required"])
		}
		if len(gotBody.Messages) != 1 || len(gotBody.Messages[0].Content) != 2 {
			t.Fatalf("expected one message with text+image_url parts, got %+v", gotBody.Messages)
		}
		if gotBody.Messages[0].Content[1].ImageURL == nil {
			t.Fatalf("expected image_url content part")
		}

		args := `{"restaurantName":"Thai Basil","date":"2026-07-05","subtotalCents":1200,"tipCents":240,"totalPaidCents":1560,"items":[{"name":"Pad Thai","priceCents":1200,"quantity":1}]}`
		resp := chatResponse{
			Choices: []responseChoice{{
				Message: responseMessage{
					ToolCalls: []responseToolCall{{
						ID:       "call_1",
						Function: toolCallFunction{Name: extractFunctionName, Arguments: args},
					}},
				},
				FinishReason: "stop",
			}},
			Usage: responseUsage{PromptTokens: 500, CompletionTokens: 80},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	result, err := c.ExtractReceipt(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q, want %q", gotModel, "test-model")
	}
	if gotRaw["tool_choice"] != "required" {
		t.Errorf("wire tool_choice = %v, want required", gotRaw["tool_choice"])
	}
	if gotRaw["prompt_cache_key"] != promptCacheKey {
		t.Errorf("prompt_cache_key = %v, want %q", gotRaw["prompt_cache_key"], promptCacheKey)
	}
	if gotBody.MaxTokens != 6000 {
		t.Errorf("max_tokens = %d, want 6000", gotBody.MaxTokens)
	}
	if result.Receipt.RestaurantName != "Thai Basil" {
		t.Errorf("restaurant name = %q, want %q", result.Receipt.RestaurantName, "Thai Basil")
	}
	if result.Receipt.SubtotalCents != 1200 || result.Receipt.TipCents != 240 || result.Receipt.TotalPaidCents != 1560 {
		t.Errorf("unexpected totals: subtotal=%d tip=%d totalPaid=%d",
			result.Receipt.SubtotalCents, result.Receipt.TipCents, result.Receipt.TotalPaidCents)
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].Name != "Pad Thai" || result.Receipt.Items[0].PriceCents != 1200 {
		t.Errorf("unexpected items: %+v", result.Receipt.Items)
	}
	if result.Usage.PromptTokens != 500 || result.Usage.CompletionTokens != 80 {
		t.Errorf("unexpected usage: %+v", result.Usage)
	}
}

func TestExtractReceiptReasoningConfigIsModelAware(t *testing.T) {
	if extractionMaxTokens != 6000 || extractionReasoningBudgetTokens != 4000 || extractionMaxTokens-extractionReasoningBudgetTokens != 2000 {
		t.Fatalf("token budget invariant changed: max=%d reasoning=%d tool-call capacity=%d", extractionMaxTokens, extractionReasoningBudgetTokens, extractionMaxTokens-extractionReasoningBudgetTokens)
	}
	tests := []struct {
		name                string
		model               string
		wantThinkingPresent bool
		wantThinkingType    string
		wantThinkingBudget  int
		wantMinimizePrompt  bool
	}{
		{
			name:                "kimi hard reasoning budget",
			model:               kimiK2P7CodeModel,
			wantThinkingPresent: true,
			wantThinkingType:    "enabled",
			wantThinkingBudget:  4000,
		},
		{
			name:                "minimax hard reasoning budget",
			model:               minimaxM3Model,
			wantThinkingPresent: true,
			wantThinkingType:    "enabled",
			wantThinkingBudget:  4000,
		},
		{
			name:               "unknown model prompt guidance",
			model:              "accounts/example/models/custom",
			wantMinimizePrompt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody chatRequest
			var gotRaw map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
				}
				if err := json.Unmarshal(body, &gotBody); err != nil {
					t.Errorf("decoding request body: %v", err)
				}
				if err := json.Unmarshal(body, &gotRaw); err != nil {
					t.Errorf("decoding raw request body: %v", err)
				}

				writeReceiptResponse(t, w, `{"items":[]}`)
			}))
			defer srv.Close()

			c := New(srv.URL, "test-key", tt.model)
			_, err := c.ExtractReceipt(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
			if err != nil {
				t.Fatalf("ExtractReceipt: %v", err)
			}

			if _, gotReasoningSent := gotRaw["reasoning_effort"]; gotReasoningSent {
				t.Fatalf("reasoning_effort must not be sent (thinking.budget_tokens is the hard-cap mechanism), got %v", gotRaw["reasoning_effort"])
			}
			gotThinking, gotThinkingPresent := gotRaw["thinking"]
			if gotThinkingPresent != tt.wantThinkingPresent {
				t.Fatalf("thinking present = %v, want %v", gotThinkingPresent, tt.wantThinkingPresent)
			}
			if tt.wantThinkingPresent {
				thinkingMap, ok := gotThinking.(map[string]any)
				if !ok {
					t.Fatalf("thinking field is not an object: %T", gotThinking)
				}
				if gotType, _ := thinkingMap["type"].(string); gotType != tt.wantThinkingType {
					t.Fatalf("thinking.type = %q, want %q", gotType, tt.wantThinkingType)
				}
				if gotBudget, _ := thinkingMap["budget_tokens"].(float64); int(gotBudget) != tt.wantThinkingBudget {
					t.Fatalf("thinking.budget_tokens = %v, want %d", gotBudget, tt.wantThinkingBudget)
				}
			}
			if gotBody.MaxTokens != 6000 {
				t.Fatalf("max_tokens = %d, want 6000", gotBody.MaxTokens)
			}
			if len(gotBody.Messages) != 1 || len(gotBody.Messages[0].Content) == 0 {
				t.Fatalf("expected prompt content, got %+v", gotBody.Messages)
			}

			gotMinimizePrompt := strings.Contains(gotBody.Messages[0].Content[0].Text, minimizeReasoningPromptSuffix)
			if gotMinimizePrompt != tt.wantMinimizePrompt {
				t.Fatalf("minimize-thinking prompt present = %v, want %v", gotMinimizePrompt, tt.wantMinimizePrompt)
			}
			// Collapse whitespace so the assertion is robust to source line-wrapping.
			normalizedPrompt := strings.Join(strings.Fields(gotBody.Messages[0].Content[0].Text), " ")
			if !strings.Contains(normalizedPrompt, "verify that the sum of each item's priceCents times its quantity equals subtotalCents") {
				t.Fatalf("expected prompt to instruct the model to reconcile items against subtotalCents, got: %q", gotBody.Messages[0].Content[0].Text)
			}
		})
	}
}

func TestExtractReceiptAttemptReturnsToolCallID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeReceiptResponseWithID(t, w, "call_abc123", `{"items":[]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	result, err := c.ExtractReceiptAttempt(context.Background(), []byte("x"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceiptAttempt: %v", err)
	}
	if result.ToolCallID != "call_abc123" {
		t.Errorf("ToolCallID = %q, want %q", result.ToolCallID, "call_abc123")
	}
	if string(result.RawArguments) != `{"items":[]}` {
		t.Errorf("RawArguments = %s, want %s", result.RawArguments, `{"items":[]}`)
	}
}

func TestExtractReceiptFeedbackSendsFullHistory(t *testing.T) {
	var gotBody chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		writeReceiptResponseWithID(t, w, "call_2", `{"items":[],"subtotalCents":1200}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	priorArgs := json.RawMessage(`{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":1}],"subtotalCents":1200}`)
	result, err := c.ExtractReceiptFeedback(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg", "call_1", priorArgs, "items sum to $24 but subtotal is $12 — re-examine the image")
	if err != nil {
		t.Fatalf("ExtractReceiptFeedback: %v", err)
	}
	if result.ToolCallID != "call_2" {
		t.Errorf("ToolCallID = %q, want %q", result.ToolCallID, "call_2")
	}

	if len(gotBody.Messages) != 4 {
		t.Fatalf("expected 4 messages (original user, assistant tool call, tool result, feedback user), got %d: %+v", len(gotBody.Messages), gotBody.Messages)
	}

	original := gotBody.Messages[0]
	if original.Role != "user" || len(original.Content) != 2 || original.Content[1].ImageURL == nil {
		t.Fatalf("message 0: expected original user message with text+image, got %+v", original)
	}

	assistant := gotBody.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("message 1: role = %q, want %q", assistant.Role, "assistant")
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Arguments != string(priorArgs) {
		t.Fatalf("message 1: expected replayed tool call with id=call_1 and prior arguments, got %+v", assistant.ToolCalls)
	}

	toolResult := gotBody.Messages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "call_1" {
		t.Fatalf("message 2: expected tool result acking call_1, got %+v", toolResult)
	}

	feedbackMsg := gotBody.Messages[3]
	if feedbackMsg.Role != "user" || len(feedbackMsg.Content) != 2 {
		t.Fatalf("message 3: expected feedback user message with text+image, got %+v", feedbackMsg)
	}
	if !strings.Contains(feedbackMsg.Content[0].Text, "re-examine the image") {
		t.Errorf("message 3: text = %q, want it to contain the feedback text", feedbackMsg.Content[0].Text)
	}
	if feedbackMsg.Content[1].ImageURL == nil {
		t.Fatal("message 3: expected the image to be re-sent on the follow-up turn")
	}
}

func TestExtractReceiptUpstreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	_, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error on non-200 upstream response")
	}
}

func TestExtractReceiptRejectsUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeReceiptResponse(t, w, `{"items":[],"unexpected_field":"should cause a decode error"}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	_, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err == nil {
		t.Fatal("expected decode to reject an unexpected field in the model's JSON output")
	}
}

func TestCalculateInterimRoundsEachItemAndDefaultsQuantity(t *testing.T) {
	got, err := calculateInterim(`{"item":[{"p":100,"n":2.5},{"p":333,"n":0}]}`)
	if err != nil {
		t.Fatalf("calculateInterim: %v", err)
	}
	if got != `{"subtotalCents":583}` {
		t.Fatalf("result = %s, want %s", got, `{"subtotalCents":583}`)
	}
}

func TestCalculateInterimAcceptsSingletonItem(t *testing.T) {
	got, err := calculateInterim(`{"item":{"p":275,"n":2}}`)
	if err != nil {
		t.Fatalf("calculateInterim: %v", err)
	}
	if got != `{"subtotalCents":550}` {
		t.Fatalf("result = %s, want %s", got, `{"subtotalCents":550}`)
	}
}

func TestCalculateInterimRejectsInvalidArguments(t *testing.T) {
	for _, raw := range []string{`{"item":[{"p":1}]}`, `{"item":[{"p":-1,"n":1}]}`, `{"item":[{"p":1,"n":"one"}]}`, `{"item":[{"p":1,"n":1,"x":2}]}`, `{"item":[]} {"extra":true}`} {
		if _, err := calculateInterim(raw); err == nil {
			t.Errorf("calculateInterim(%s) accepted invalid arguments", raw)
		}
	}
}

func TestExtractReceiptCalculatorTurn(t *testing.T) {
	var requests []chatRequest
	var rawRequests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoded, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		var body chatRequest
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, body)
		var raw map[string]any
		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatalf("decode raw request: %v", err)
		}
		rawRequests = append(rawRequests, raw)
		if len(requests) == 1 {
			writeToolResponse(t, w, "calc_1", interimCalculationFunctionName, `{"item":[{"p":100,"n":2.5},{"p":333,"n":0}]}`, responseUsage{PromptTokens: 11, CompletionTokens: 7})
			return
		}
		writeToolResponse(t, w, "extract_2", extractFunctionName, `{"restaurantName":"Cafe","subtotalCents":583,"items":[{"name":"Item","priceCents":100,"quantity":2.5}]}`, responseUsage{PromptTokens: 23, CompletionTokens: 13})
	}))
	defer srv.Close()

	result, err := New(srv.URL, "test-key", "test-model").ExtractReceipt(context.Background(), []byte("image"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(requests))
	}
	if !requests[0].ToolChoice.Required || requests[1].ToolChoice.Function.Name != extractFunctionName {
		t.Fatalf("unexpected tool choices: %+v, %+v", requests[0].ToolChoice, requests[1].ToolChoice)
	}
	if requests[0].Tools[0].Function.Name != requests[1].Tools[0].Function.Name || requests[0].Tools[1].Function.Name != requests[1].Tools[1].Function.Name {
		t.Fatalf("tools changed between turns: %+v vs %+v", requests[0].Tools, requests[1].Tools)
	}
	if rawRequests[0]["tool_choice"] != "required" || rawRequests[1]["prompt_cache_key"] != promptCacheKey {
		t.Fatalf("unexpected wire controls: first choice=%v final cache key=%v", rawRequests[0]["tool_choice"], rawRequests[1]["prompt_cache_key"])
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("final messages = %d, want user + assistant call + tool result", len(requests[1].Messages))
	}
	toolResult := requests[1].Messages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != "calc_1" || len(toolResult.Content) != 1 || toolResult.Content[0].Text != `{"subtotalCents":583}` {
		t.Fatalf("unexpected calculator result: %+v", toolResult)
	}
	if result.Receipt.RestaurantName != "Cafe" || result.Receipt.SubtotalCents != 583 {
		t.Fatalf("unexpected receipt: %+v", result.Receipt)
	}
	if result.Usage.PromptTokens != 34 || result.Usage.CompletionTokens != 20 {
		t.Fatalf("usage = %+v, want 34 prompt/20 completion", result.Usage)
	}
}

func writeToolResponse(t *testing.T, w http.ResponseWriter, id, name, args string, usage responseUsage) {
	t.Helper()
	resp := chatResponse{Choices: []responseChoice{{Message: responseMessage{ToolCalls: []responseToolCall{{ID: id, Function: toolCallFunction{Name: name, Arguments: args}}}}, FinishReason: "stop"}}, Usage: usage}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encoding response: %v", err)
	}
}

func writeReceiptResponse(t *testing.T, w http.ResponseWriter, args string) {
	t.Helper()
	writeReceiptResponseWithID(t, w, "call_1", args)
}

func writeReceiptResponseWithID(t *testing.T, w http.ResponseWriter, id, args string) {
	t.Helper()

	resp := chatResponse{
		Choices: []responseChoice{{
			Message: responseMessage{
				ToolCalls: []responseToolCall{{
					ID:       id,
					Function: toolCallFunction{Name: extractFunctionName, Arguments: args},
				}},
			},
			FinishReason: "stop",
		}},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encoding response: %v", err)
	}
}

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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		gotModel = gotBody.Model

		if gotBody.ToolChoice.Function.Name != extractFunctionName {
			t.Errorf("expected tool_choice targeting %q, got %q", extractFunctionName, gotBody.ToolChoice.Function.Name)
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
	if gotBody.MaxTokens != extractionMaxTokens {
		t.Errorf("max_tokens = %d, want %d", gotBody.MaxTokens, extractionMaxTokens)
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
			wantThinkingBudget:  extractionReasoningBudgetTokens,
		},
		{
			name:                "minimax hard reasoning budget",
			model:               minimaxM3Model,
			wantThinkingPresent: true,
			wantThinkingType:    "enabled",
			wantThinkingBudget:  extractionReasoningBudgetTokens,
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
			if gotBody.MaxTokens != extractionMaxTokens {
				t.Fatalf("max_tokens = %d, want %d", gotBody.MaxTokens, extractionMaxTokens)
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

func TestExtractReceiptToleratesFinishReasonLength(t *testing.T) {
	// The extraction path must NOT error on finish_reason="length"; it logs
	// and proceeds with whatever tool-call JSON it got.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []responseChoice{{
				Message: responseMessage{
					ToolCalls: []responseToolCall{{
						ID:       "call_len",
						Function: toolCallFunction{Name: extractFunctionName, Arguments: `{"items":[]}`},
					}},
				},
				FinishReason: "length",
			}},
			Usage: responseUsage{PromptTokens: 100, CompletionTokens: 4000},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	_, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt must tolerate finish_reason=length: %v", err)
	}
}

func TestExtractReceiptToleratesMultipleToolCalls(t *testing.T) {
	// The extraction path must use the first tool call and ignore extras.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []responseChoice{{
				Message: responseMessage{
					ToolCalls: []responseToolCall{
						{
							ID:       "call_1",
							Function: toolCallFunction{Name: extractFunctionName, Arguments: `{"items":[{"name":"First","priceCents":100,"quantity":1}],"subtotalCents":100}`},
						},
						{
							ID:       "call_2",
							Function: toolCallFunction{Name: extractFunctionName, Arguments: `{"items":[{"name":"Second","priceCents":200,"quantity":1}],"subtotalCents":200}`},
						},
					},
				},
				FinishReason: "stop",
			}},
			Usage: responseUsage{PromptTokens: 100, CompletionTokens: 50},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "test-model")
	result, err := c.ExtractReceipt(context.Background(), []byte("x"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt must tolerate multiple tool calls: %v", err)
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].Name != "First" {
		t.Fatalf("expected first tool call's data, got %+v", result.Receipt.Items)
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

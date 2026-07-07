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
		resp := chatResponse{}
		resp.Choices = make([]struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		}, 1)
		resp.Choices[0].Message.ToolCalls = make([]struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}, 1)
		resp.Choices[0].Message.ToolCalls[0].Function.Name = extractFunctionName
		resp.Choices[0].Message.ToolCalls[0].Function.Arguments = args
		resp.Usage.PromptTokens = 500
		resp.Usage.CompletionTokens = 80

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
		name                 string
		model                string
		wantReasoningPresent bool
		wantReasoning        string
		wantMinimizePrompt   bool
	}{
		{
			name:                 "kimi low reasoning effort",
			model:                kimiK2P7CodeModel,
			wantReasoningPresent: true,
			wantReasoning:        "low",
		},
		{
			name:               "minimax prompt guidance",
			model:              "accounts/fireworks/models/minimax-m3",
			wantMinimizePrompt: true,
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

			gotReasoning, gotReasoningPresent := gotRaw["reasoning_effort"]
			if gotReasoningPresent != tt.wantReasoningPresent {
				t.Fatalf("reasoning_effort present = %v, want %v", gotReasoningPresent, tt.wantReasoningPresent)
			}
			if tt.wantReasoningPresent && gotReasoning != tt.wantReasoning {
				t.Fatalf("reasoning_effort = %v, want %q", gotReasoning, tt.wantReasoning)
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
		resp := chatResponse{}
		resp.Choices = make([]struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		}, 1)
		resp.Choices[0].Message.ToolCalls = make([]struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}, 1)
		resp.Choices[0].Message.ToolCalls[0].Function.Name = extractFunctionName
		resp.Choices[0].Message.ToolCalls[0].Function.Arguments = `{"items":[],"unexpected_field":"should cause a decode error"}`
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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

	resp := chatResponse{}
	resp.Choices = make([]struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}, 1)
	resp.Choices[0].Message.ToolCalls = make([]struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}, 1)
	resp.Choices[0].Message.ToolCalls[0].Function.Name = extractFunctionName
	resp.Choices[0].Message.ToolCalls[0].Function.Arguments = args

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encoding response: %v", err)
	}
}

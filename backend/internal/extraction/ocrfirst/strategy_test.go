package ocrfirst

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"share/backend/internal/llm/openaicompat"
)

const fakeMinimaxModel = "accounts/fireworks/models/minimax-m3"

type stubOCR struct {
	text string
	err  error
}

func (s stubOCR) Extract(ctx context.Context, image []byte) (string, error) {
	return s.text, s.err
}

func writeToolCallResponse(w http.ResponseWriter, id, args string) {
	resp := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]any{
					"tool_calls": []map[string]any{
						{"id": id, "function": map[string]any{"name": "extract_receipt", "arguments": args}},
					},
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{"prompt_tokens": 300, "completion_tokens": 60},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestRunStructuresOCRTextAndReportsMatch(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		args := `{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":1}],"subtotalCents":1200}`
		writeToolCallResponse(w, "call_1", args)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(stubOCR{text: "PAD THAI 12.00\nSUBTOTAL 12.00"}, client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(result.Attempts))
	}
	if result.SubtotalMatched == nil || !*result.SubtotalMatched {
		t.Fatalf("SubtotalMatched = %v, want true", result.SubtotalMatched)
	}

	// The structuring call must be text-only — no image_url content part —
	// and must carry the OCR'd text in the prompt.
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 message on a text-only structuring call, got %d", len(messages))
	}
	msg, _ := messages[0].(map[string]any)
	content, _ := msg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected exactly 1 content part (text only, no image), got %+v", content)
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "text" {
		t.Fatalf("content part type = %v, want text", part["type"])
	}
	text, _ := part["text"].(string)
	if !strings.Contains(text, "PAD THAI 12.00") {
		t.Fatalf("expected structuring prompt to contain the OCR'd text, got: %q", text)
	}
}

func TestRunReturnsErrorOnOCRFailure(t *testing.T) {
	client := openaicompat.New("http://unused.invalid", "test-key", fakeMinimaxModel)
	strategy := New(stubOCR{err: errors.New("tesseract: boom")}, client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when OCR fails")
	}
	if len(result.Attempts) != 0 {
		t.Fatalf("expected 0 attempts when OCR fails before any LLM call, got %d", len(result.Attempts))
	}
}

func TestRunReturnsErrorOnEmptyOCRText(t *testing.T) {
	client := openaicompat.New("http://unused.invalid", "test-key", fakeMinimaxModel)
	strategy := New(stubOCR{text: "   \n  "}, client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	_, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when OCR produces only whitespace")
	}
}

func TestRunReportsFailedStructuringCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(stubOCR{text: "some receipt text"}, client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when the structuring call fails")
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Err == nil {
		t.Fatalf("expected 1 failed attempt, got %+v", result.Attempts)
	}
}

func TestNameAndMaxCalls(t *testing.T) {
	client := openaicompat.New("http://unused.invalid", "test-key", fakeMinimaxModel)
	strategy := New(stubOCR{}, client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)
	if strategy.Name() != "ocr_first" {
		t.Fatalf("Name() = %q, want ocr_first", strategy.Name())
	}
	if strategy.MaxCalls() != 1 {
		t.Fatalf("MaxCalls() = %d, want 1", strategy.MaxCalls())
	}
}

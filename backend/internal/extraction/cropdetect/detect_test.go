package cropdetect

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"share/backend/internal/llm/openaicompat"
)

const fakeMinimaxModel = "accounts/fireworks/models/minimax-m3"

func writeToolCallResponse(w http.ResponseWriter, functionName, id, args string) {
	resp := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]any{
					"tool_calls": []map[string]any{
						{"id": id, "function": map[string]any{"name": functionName, "arguments": args}},
					},
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{"prompt_tokens": 400, "completion_tokens": 50},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestDetectParsesValidBounds(t *testing.T) {
	var gotFunctionName string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		// Verify the request uses the detection function name.
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(tools))
		}
		tool, _ := tools[0].(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		gotFunctionName, _ = fn["name"].(string)

		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":50,"minY":100,"maxX":900,"maxY":850},"confidence":0.95}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	result, usage, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if gotFunctionName != DetectionFunctionName {
		t.Fatalf("function name = %q, want %q", gotFunctionName, DetectionFunctionName)
	}

	if result.Bounds.MinX != 50 || result.Bounds.MinY != 100 || result.Bounds.MaxX != 900 || result.Bounds.MaxY != 850 {
		t.Fatalf("bounds = %+v, want {50 100 900 850}", result.Bounds)
	}

	if result.Confidence == nil || *result.Confidence != 0.95 {
		t.Fatalf("confidence = %v, want 0.95", result.Confidence)
	}

	if usage.PromptTokens != 400 || usage.CompletionTokens != 50 {
		t.Fatalf("usage = %+v, want {400 50}", usage)
	}
}

func TestDetectAcceptsMissingConfidence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":0,"minY":0,"maxX":1000,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	result, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if result.Confidence != nil {
		t.Fatalf("expected confidence to be nil when omitted, got %v", *result.Confidence)
	}
}

func TestDetectRejectsBoundsOutOfRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":-10,"minY":0,"maxX":1000,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for out-of-range bounds")
	}
	if !strings.Contains(err.Error(), "outside 0..1000") {
		t.Fatalf("error = %q, want bounds-outside-range error", err.Error())
	}
}

func TestDetectRejectsMinGEMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":500,"minY":0,"maxX":400,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for min >= max bounds")
	}
}

func TestDetectRejectsUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100},"extra":true}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for unknown fields")
	}
}

func TestDetectRejectsWrongFunctionName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a response under a different function name — this should
		// fail at the openaicompat layer.
		writeToolCallResponse(w, "other_function", "call_1", `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for wrong function name")
	}
}

func TestDetectSupportsThinkingModels(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":0,"minY":0,"maxX":1000,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// MinimaxM3 supports thinking, so the thinking config should be present.
	if _, ok := gotBody["thinking"]; !ok {
		t.Fatal("expected thinking config for a thinking-supported model")
	}
}

func TestDetectOmitsThinkingForUnknownModel(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		writeToolCallResponse(w, DetectionFunctionName, "call_detect_1", `{"bounds":{"minX":0,"minY":0,"maxX":1000,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", "accounts/example/models/custom")
	_, _, err := Detect(context.Background(), client, "accounts/example/models/custom", []byte("fake-image"), "image/jpeg", 512)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if _, ok := gotBody["thinking"]; ok {
		t.Fatal("expected no thinking config for an unsupported model")
	}

	// Should also include the minimize-reasoning prompt suffix.
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	msg, _ := messages[0].(map[string]any)
	content, _ := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 content parts (text + image), got %d", len(content))
	}
	part0, _ := content[0].(map[string]any)
	text, _ := part0["text"].(string)
	if !strings.Contains(text, openaicompat.MinimizeReasoningPromptSuffix) {
		t.Fatalf("expected minimize-reasoning suffix for non-thinking model, got: %q", text)
	}
}

func TestValidationRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name   string
		bounds Bounds
	}{
		{"negative minX", Bounds{MinX: -1, MinY: 0, MaxX: 100, MaxY: 100}},
		{"negative minY", Bounds{MinX: 0, MinY: -1, MaxX: 100, MaxY: 100}},
		{"maxX > 1000", Bounds{MinX: 0, MinY: 0, MaxX: 1001, MaxY: 100}},
		{"maxY > 1000", Bounds{MinX: 0, MinY: 0, MaxX: 100, MaxY: 1001}},
		{"minX >= maxX", Bounds{MinX: 100, MinY: 0, MaxX: 100, MaxY: 100}},
		{"minY >= maxY", Bounds{MinX: 0, MinY: 100, MaxX: 100, MaxY: 100}},
		{"minX > maxX", Bounds{MinX: 200, MinY: 0, MaxX: 100, MaxY: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DetectionResult{Bounds: tt.bounds}
			if err := d.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidationOK(t *testing.T) {
	d := &DetectionResult{Bounds: Bounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000}}
	if err := d.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	d2 := &DetectionResult{Bounds: Bounds{MinX: 50, MinY: 100, MaxX: 900, MaxY: 850}}
	if err := d2.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestDetectSendsMaxTokens512(t *testing.T) {
	var gotMaxTokens float64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		gotMaxTokens, _ = body["max_tokens"].(float64)
		writeToolCallResponse(w, DetectionFunctionName, "call_1", `{"bounds":{"minX":0,"minY":0,"maxX":1000,"maxY":1000}}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", "test-model")
	_, _, err := Detect(context.Background(), client, "test-model", []byte("fake-image"), "image/jpeg", 0)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if int(gotMaxTokens) != 512 {
		t.Fatalf("max_tokens = %v, want 512", gotMaxTokens)
	}
}

func TestValidationRejectsConfidenceOutOfRange(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
	}{
		{"negative", -0.1},
		{"greater than 1", 1.5},
		{"NaN", math.NaN()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.confidence
			d := &DetectionResult{
				Bounds:     Bounds{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100},
				Confidence: &c,
			}
			if err := d.Validate(); err == nil {
				t.Fatalf("expected validation error for confidence=%v", tt.confidence)
			}
		})
	}
}

func TestDetectRejectsFinishReasonLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"tool_calls": []map[string]any{
							{"id": "call_1", "function": map[string]any{"name": DetectionFunctionName, "arguments": `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100}}`}},
						},
					},
					"finish_reason": "length",
				},
			},
			"usage": map[string]any{"prompt_tokens": 400, "completion_tokens": 512},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for finish_reason=length in strict mode")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error = %q, want truncated error", err.Error())
	}
}

func TestDetectRejectsMultipleToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"tool_calls": []map[string]any{
							{"id": "call_1", "function": map[string]any{"name": DetectionFunctionName, "arguments": `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100}}`}},
							{"id": "call_2", "function": map[string]any{"name": DetectionFunctionName, "arguments": `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100}}`}},
						},
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 400, "completion_tokens": 50},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for multiple tool calls in strict mode")
	}
	if !strings.Contains(err.Error(), "want exactly 1") {
		t.Fatalf("error = %q, want 'want exactly 1'", err.Error())
	}
}

func TestDetectRejectsConfidenceOutOfRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToolCallResponse(w, DetectionFunctionName, "call_1", `{"bounds":{"minX":0,"minY":0,"maxX":100,"maxY":100},"confidence":1.5}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	_, _, err := Detect(context.Background(), client, fakeMinimaxModel, []byte("fake-image"), "image/jpeg", 512)
	if err == nil {
		t.Fatal("expected an error for confidence > 1")
	}
}

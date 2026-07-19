package feedback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

const fakeMinimaxModel = "accounts/fireworks/models/minimax-m3"

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
		"usage": map[string]any{"prompt_tokens": 500, "completion_tokens": 80},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestRunSkipsRetryWhenSubtotalMatches(t *testing.T) {
	var callCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		args := `{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":1}],"subtotalCents":1200}`
		writeToolCallResponse(w, "call_1", args)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected exactly 1 LLM call when the first attempt already matches, got %d", callCount)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(result.Attempts))
	}
	if result.SubtotalMatched == nil || !*result.SubtotalMatched {
		t.Fatalf("SubtotalMatched = %v, want true", result.SubtotalMatched)
	}
}

func TestRunSkipsRetryWhenEverythingIsUnverified(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeToolCallResponse(w, "call_1", `{"items":[{"name":"item","priceCents":100,"quantity":1}]}`)
	}))
	defer srv.Close()
	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	result, err := New(client, "test", fakeMinimaxModel, 1, 1).Run(context.Background(), nil, "image/jpeg")
	if err != nil || calls != 1 || len(result.Attempts) != 1 || result.Reconciliation.FailedChecks != 0 {
		t.Fatalf("unverified result should not retry: calls=%d err=%v result=%+v", calls, err, result)
	}
}

func TestRunRetriesOnTaxAndGrandTotalMismatches(t *testing.T) {
	for _, tc := range []struct {
		name, first string
	}{
		{"tax", `{"items":[{"name":"item","priceCents":1000,"quantity":1}],"subtotalCents":1000,"taxCents":50,"taxRateBasisPoints":1000,"tipKnown":true,"totalPaidCents":1050}`},
		{"grand total", `{"items":[{"name":"item","priceCents":1000,"quantity":1}],"subtotalCents":1000,"taxCents":100,"tipKnown":true,"totalPaidCents":1050}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeToolCallResponse(w, "call", tc.first)
			}))
			defer srv.Close()
			client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
			result, err := New(client, "test", fakeMinimaxModel, 1, 1).Run(context.Background(), nil, "image/jpeg")
			if err != nil || calls != 2 || len(result.Attempts) != 2 {
				t.Fatalf("mismatch should retry: calls=%d err=%v attempts=%d", calls, err, len(result.Attempts))
			}
		})
	}
}

func TestBetterRetainsFirstOnTieAndSelectsOnlyStrictlyBetter(t *testing.T) {
	a := extraction.Reconciliation{FailedChecks: 1, AggregateAbsDifferenceCents: 10}
	if better(a, a) {
		t.Fatal("equal reconciliation scores must not be considered better")
	}
	if !better(extraction.Reconciliation{}, a) {
		t.Fatal("fewer failed checks should be better")
	}
}

func TestRunRetainsFirstAttemptOnExactTie(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeToolCallResponse(w, "call", `{"items":[{"name":"same","priceCents":1200,"quantity":2}],"subtotalCents":1200}`)
	}))
	defer srv.Close()
	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	result, err := New(client, "test", fakeMinimaxModel, 1, 1).Run(context.Background(), nil, "image/jpeg")
	if err != nil || calls != 2 || result.Receipt.Items[0].Name != "same" {
		t.Fatalf("expected retry with first retained on tie: calls=%d err=%v receipt=%+v", calls, err, result.Receipt)
	}
}

func TestMismatchFeedbackOnlyReportsCheckedMismatches(t *testing.T) {
	rec := extraction.Reconciliation{}
	text := mismatchFeedback(rec, llm.ExtractedReceipt{})
	if strings.Contains(text, "items sum") || strings.Contains(text, "differs from the total") {
		t.Fatalf("unchecked mismatches leaked into feedback: %q", text)
	}
}

func TestRunRetriesOnMismatchWithFullHistoryAndUsesSecondAttempt(t *testing.T) {
	var callCount int
	var gotBodies []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		gotBodies = append(gotBodies, body)

		if callCount == 1 {
			// Items sum to 2400 but subtotal says 1200 — a mismatch that
			// should trigger the retry.
			args := `{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":2}],"subtotalCents":1200}`
			writeToolCallResponse(w, "call_1", args)
			return
		}
		args := `{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":1}],"subtotalCents":1200}`
		writeToolCallResponse(w, "call_2", args)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected exactly 2 LLM calls on a first-attempt mismatch, got %d", callCount)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}
	if result.SubtotalMatched == nil || !*result.SubtotalMatched {
		t.Fatalf("SubtotalMatched = %v, want true (from the corrected second attempt)", result.SubtotalMatched)
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].Quantity != 1 {
		t.Fatalf("expected the final receipt to be the second attempt's corrected data, got %+v", result.Receipt.Items)
	}

	// The second request must carry the first attempt as real conversation
	// history: original user turn, replayed assistant tool call, tool
	// result, then a new user turn with feedback text and the image again.
	secondMessages, _ := gotBodies[1]["messages"].([]any)
	if len(secondMessages) != 4 {
		t.Fatalf("expected 4 messages on the retry call, got %d: %+v", len(secondMessages), secondMessages)
	}
	assistantMsg, _ := secondMessages[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Fatalf("message 1 role = %v, want assistant", assistantMsg["role"])
	}
	toolCalls, _ := assistantMsg["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("expected replayed tool call, got %+v", assistantMsg["tool_calls"])
	}
	replayedCall, _ := toolCalls[0].(map[string]any)
	if replayedCall["id"] != "call_1" {
		t.Fatalf("replayed tool_call id = %v, want call_1", replayedCall["id"])
	}

	toolMsg, _ := secondMessages[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Fatalf("message 2 = %+v, want tool result acking call_1", toolMsg)
	}

	feedbackMsg, _ := secondMessages[3].(map[string]any)
	if feedbackMsg["role"] != "user" {
		t.Fatalf("message 3 role = %v, want user", feedbackMsg["role"])
	}
	content, _ := feedbackMsg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected feedback message to carry text+image, got %+v", content)
	}
	part0, _ := content[0].(map[string]any)
	feedbackText, _ := part0["text"].(string)
	if !strings.Contains(feedbackText, "$24.00") || !strings.Contains(feedbackText, "$12.00") {
		t.Fatalf("expected feedback text to quote the specific computed/printed amounts, got: %q", feedbackText)
	}
}

func TestRunFallsBackToFirstAttemptWhenRetryCallFails(t *testing.T) {
	var callCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			args := `{"items":[{"name":"Pad Thai","priceCents":1200,"quantity":2}],"subtotalCents":1200}`
			writeToolCallResponse(w, "call_1", args)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("fake-jpeg-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v, want no error (retry failure should fall back to the first attempt)", err)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts (first success, second failed), got %d", len(result.Attempts))
	}
	if result.Attempts[1].Err == nil {
		t.Fatal("expected the second attempt to record its error")
	}
	if len(result.Receipt.Items) != 1 || result.Receipt.Items[0].Quantity != 2 {
		t.Fatalf("expected the fallback receipt to be the first attempt's data, got %+v", result.Receipt.Items)
	}
	if result.SubtotalMatched == nil || *result.SubtotalMatched {
		t.Fatalf("SubtotalMatched = %v, want false (first attempt's own verdict)", result.SubtotalMatched)
	}
}

func TestRunReportsFailedFirstAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key", fakeMinimaxModel)
	strategy := New(client, "fireworks:"+fakeMinimaxModel, fakeMinimaxModel, 1, 1)

	result, err := strategy.Run(context.Background(), []byte("x"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when the first call fails")
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Err == nil {
		t.Fatalf("expected 1 failed attempt, got %+v", result.Attempts)
	}
}

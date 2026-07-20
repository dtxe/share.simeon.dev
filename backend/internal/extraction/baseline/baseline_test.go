package baseline

import (
	"context"
	"errors"
	"testing"

	"share/backend/internal/llm"
)

type fakeProvider struct {
	result *llm.Result
	err    error
}

func (f fakeProvider) ExtractReceipt(context.Context, []byte, string) (*llm.Result, error) {
	return f.result, f.err
}

func (fakeProvider) Name() string { return "fake" }

func TestRunReportsSuccessfulAttempt(t *testing.T) {
	strategy := New(fakeProvider{result: &llm.Result{
		Receipt: llm.ExtractedReceipt{
			SubtotalCents: 500,
			Items:         []llm.ExtractedItem{{Name: "item", PriceCents: 500, Quantity: 1}},
		},
		Usage: llm.Usage{PromptTokens: 1000, CompletionTokens: 1000},
	}}, "fake-model", 1, 1)

	result, err := strategy.Run(context.Background(), []byte("image"), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(result.Attempts))
	}
	attempt := result.Attempts[0]
	if attempt.Provider != "fake" || attempt.Model != "fake-model" {
		t.Fatalf("attempt provider/model = %q/%q", attempt.Provider, attempt.Model)
	}
	if attempt.CostCents == nil || *attempt.CostCents != 2 {
		t.Fatalf("attempt cost = %v, want 2", attempt.CostCents)
	}
	if attempt.SubtotalMatched == nil || !*attempt.SubtotalMatched {
		t.Fatalf("attempt subtotal match = %v, want true", attempt.SubtotalMatched)
	}
	if len(attempt.RawJSON) == 0 {
		t.Fatal("attempt raw JSON is empty")
	}
}

func TestRunPreservesFailedAttemptMetadata(t *testing.T) {
	wantErr := errors.New("provider failed")
	strategy := New(fakeProvider{err: wantErr}, "fake-model", 1, 1)

	result, err := strategy.Run(context.Background(), []byte("image"), "image/jpeg")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(result.Attempts))
	}
	attempt := result.Attempts[0]
	if attempt.Provider != "fake" || attempt.Model != "fake-model" || !errors.Is(attempt.Err, wantErr) {
		t.Fatalf("failed attempt = %+v", attempt)
	}
	if attempt.CostCents != nil {
		t.Fatalf("failed attempt cost = %v, want unknown", attempt.CostCents)
	}
}

func TestRunMarksOnlyFinalCapturedTurnAsFailed(t *testing.T) {
	wantErr := errors.New("final provider turn failed")
	strategy := New(fakeProvider{err: &llm.ResponseError{
		Err: wantErr,
		Attempts: []llm.Attempt{
			{RawResponse: []byte(`{"first":true}`), Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 2}},
			{RawResponse: []byte(`{"second":true}`)},
		},
	}}, "fake-model", 1, 1)

	result, err := strategy.Run(context.Background(), []byte("image"), "image/jpeg")
	if !errors.Is(err, wantErr) || len(result.Attempts) != 2 {
		t.Fatalf("Run = %v, attempts=%d", err, len(result.Attempts))
	}
	if result.Attempts[0].Err != nil || !errors.Is(result.Attempts[1].Err, wantErr) {
		t.Fatalf("turn errors = %v, %v", result.Attempts[0].Err, result.Attempts[1].Err)
	}
}

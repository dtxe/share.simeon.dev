package httpapi

import (
	"testing"

	"share/backend/internal/llm"
)

func TestEstimateCostCentsUsesInputAndOutputRates(t *testing.T) {
	got := estimateCostCents(llm.Usage{PromptTokens: 10_000, CompletionTokens: 3_000}, 0.095, 0.4)
	if got != 2 {
		t.Fatalf("estimateCostCents = %d, want 2", got)
	}
}

func TestEstimateCostCentsRoundsRatherThanTruncates(t *testing.T) {
	got := estimateCostCents(llm.Usage{PromptTokens: 500, CompletionTokens: 0}, 1, 10)
	if got != 1 {
		t.Fatalf("estimateCostCents = %d, want 1", got)
	}
}

package extraction

import "testing"

func TestEstimateCostCentsUsesInputAndOutputRates(t *testing.T) {
	got := EstimateCostCents(10_000, 3_000, 0.095, 0.4)
	if got != 2 {
		t.Fatalf("EstimateCostCents = %d, want 2", got)
	}
}

func TestEstimateCostCentsRoundsRatherThanTruncates(t *testing.T) {
	got := EstimateCostCents(500, 0, 1, 10)
	if got != 1 {
		t.Fatalf("EstimateCostCents = %d, want 1", got)
	}
}

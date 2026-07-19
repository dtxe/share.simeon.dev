package extraction

import (
	"math"
	"testing"

	"share/backend/internal/llm"
)

func TestCheckSubtotal(t *testing.T) {
	cases := []struct {
		name          string
		items         []llm.ExtractedItem
		subtotalCents int64
		wantMatched   bool
		wantDiff      int64
	}{
		{
			name: "exact match",
			items: []llm.ExtractedItem{
				{Name: "burger", PriceCents: 1200, Quantity: 1},
				{Name: "fries", PriceCents: 500, Quantity: 1},
			},
			subtotalCents: 1700,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "quantity multiplies price",
			items: []llm.ExtractedItem{
				{Name: "soda", PriceCents: 300, Quantity: 3},
			},
			subtotalCents: 900,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "off by rounding within tolerance",
			items: []llm.ExtractedItem{
				{Name: "a", PriceCents: 333, Quantity: 1},
				{Name: "b", PriceCents: 333, Quantity: 1},
				{Name: "c", PriceCents: 334, Quantity: 1},
			},
			subtotalCents: 1001,
			wantMatched:   true,
			wantDiff:      1,
		},
		{
			name: "no match",
			items: []llm.ExtractedItem{
				{Name: "burger", PriceCents: 1200, Quantity: 1},
			},
			subtotalCents: 500,
			wantMatched:   false,
			wantDiff:      700,
		},
		{
			name:          "zero items",
			items:         nil,
			subtotalCents: 0,
			wantMatched:   true,
			wantDiff:      0,
		},
		{
			name: "single item",
			items: []llm.ExtractedItem{
				{Name: "coffee", PriceCents: 400, Quantity: 1},
			},
			subtotalCents: 400,
			wantMatched:   true,
			wantDiff:      0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, diff := CheckSubtotal(tc.items, tc.subtotalCents)
			if matched != tc.wantMatched || diff != tc.wantDiff {
				t.Fatalf("CheckSubtotal() = (%v, %d), want (%v, %d)", matched, diff, tc.wantMatched, tc.wantDiff)
			}
		})
	}
}

func TestReconcileTaxAndTotals(t *testing.T) {
	falseValue := false
	tax := int64(130)
	rate := int64(1300)
	tipKnown := true
	receipt := llm.ExtractedReceipt{
		Items:         []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}, {PriceCents: 500, Quantity: 1, Taxable: &falseValue}},
		SubtotalCents: 1500, TaxCents: &tax, TaxRateBasisPoints: &rate, TipCents: 200, TipKnown: &tipKnown, TotalPaidCents: 1830,
	}
	r := Reconcile(&receipt)
	if r.ItemSumCents != 1500 || r.TaxableSubtotalCents != 1000 || r.TaxSource != TaxSourcePrinted {
		t.Fatalf("unexpected reconciliation: %+v", r)
	}
	if r.FailedChecks != 0 || !r.GrandTotalMatched {
		t.Fatalf("expected all checks to pass: %+v", r)
	}
}

func TestReconcileInfersResidualTaxConservatively(t *testing.T) {
	tipKnown, noAdjustments := true, false
	r := Reconcile(&llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}}, SubtotalCents: 1000, TipCents: 100, TipKnown: &tipKnown, HasNonTaxAdjustments: &noAdjustments, TotalPaidCents: 1180})
	if r.TaxSource != TaxSourceTotalInferred || r.ResolvedTaxCents == nil || *r.ResolvedTaxCents != 80 || !r.GrandTotalMatched {
		t.Fatalf("unexpected residual inference: %+v", r)
	}
}

func TestReconcileOmittedTaxableDefaultsTrue(t *testing.T) {
	r := llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 42, Quantity: 1}}}
	Reconcile(&r)
	if r.Items[0].Taxable == nil || !*r.Items[0].Taxable {
		t.Fatal("omitted taxable marker was not normalized to true")
	}
}

func TestReconcileLeavesUnverifiableChecksUnchecked(t *testing.T) {
	r := Reconcile(&llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 100, Quantity: 1}}})
	if r.ItemSubtotalChecked || r.TaxChecked || r.GrandTotalChecked || r.FailedChecks != 0 {
		t.Fatalf("missing receipt values should be unchecked: %+v", r)
	}
}

func TestRoundBasisPointsCentsUsesIntegerRounding(t *testing.T) {
	for _, tc := range []struct{ cents, rate, want int64 }{{100, 750, 8}, {101, 5000, 51}, {1, 4999, 0}} {
		got, ok := RoundBasisPointsCents(tc.cents, tc.rate)
		if !ok || got != tc.want {
			t.Fatalf("RoundBasisPointsCents(%d,%d)=(%d,%v), want %d,true", tc.cents, tc.rate, got, ok, tc.want)
		}
	}
	for _, tc := range []struct {
		cents, rate int64
	}{
		{-1, 100}, {1, -1}, {math.MaxInt64, 10001}, {math.MaxInt64, math.MaxInt64},
	} {
		if _, ok := RoundBasisPointsCents(tc.cents, tc.rate); ok {
			t.Fatalf("RoundBasisPointsCents(%d,%d) accepted invalid input", tc.cents, tc.rate)
		}
	}
	if got, ok := RoundBasisPointsCents(math.MaxInt64, 0); !ok || got != 0 {
		t.Fatalf("zero rate = (%d,%v), want (0,true)", got, ok)
	}
}

func TestReconcileInferenceGuardsAndDiagnostics(t *testing.T) {
	trueValue, falseValue := true, false
	tests := []struct {
		name       string
		receipt    llm.ExtractedReceipt
		wantSource TaxSource
		wantFailed bool
	}{
		{"rate inferred", llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}}, TaxRateBasisPoints: ptr(int64(750))}, TaxSourceRateInferred, false},
		{"unknown tip blocks residual", llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}}, SubtotalCents: 1000, TotalPaidCents: 1100, TipCents: 0, HasNonTaxAdjustments: &falseValue}, TaxSourceUnresolved, false},
		{"adjustments block residual", llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}}, SubtotalCents: 1000, TotalPaidCents: 1100, TipKnown: &trueValue, HasNonTaxAdjustments: &trueValue}, TaxSourceUnresolved, false},
		{"negative tax fails", llm.ExtractedReceipt{TaxCents: ptr(int64(-1))}, TaxSourceUnresolved, true},
		{"negative rate fails", llm.ExtractedReceipt{TaxRateBasisPoints: ptr(int64(-1))}, TaxSourceUnresolved, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Reconcile(&tc.receipt)
			if r.TaxSource != tc.wantSource || (r.FailedChecks > 0) != tc.wantFailed {
				t.Fatalf("Reconcile() = %+v", r)
			}
		})
	}

	multiple := true
	r := Reconcile(&llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 1000, Quantity: 1}}, TaxRateBasisPoints: ptr(int64(750)), MultipleTaxRatesDetected: &multiple})
	if !r.MultipleTaxRatesDetected || r.TaxSource != TaxSourceUnresolved {
		t.Fatalf("multiple tax rates should propagate and block single-rate inference: %+v", r)
	}

	grand := Reconcile(&llm.ExtractedReceipt{SubtotalCents: 1000, TaxCents: ptr(int64(100)), TipCents: 0, TipKnown: &trueValue, TotalPaidCents: 1200})
	if !grand.GrandTotalChecked || grand.GrandTotalMatched || grand.GrandTotalDiffCents != 100 {
		t.Fatalf("grand-total mismatch not reported: %+v", grand)
	}
}

func TestReconcileDoesNotCheckMissingOrZeroSubtotal(t *testing.T) {
	for _, subtotal := range []int64{0} {
		r := Reconcile(&llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: 100, Quantity: 1}}, SubtotalCents: subtotal})
		if r.ItemSubtotalChecked || r.FailedChecks != 0 {
			t.Fatalf("subtotal %d should be unchecked: %+v", subtotal, r)
		}
	}
}

func TestReconcileRejectsInvalidItemAndLargeResidualArithmetic(t *testing.T) {
	negative := Reconcile(&llm.ExtractedReceipt{Items: []llm.ExtractedItem{{PriceCents: -1, Quantity: 1}}})
	if negative.FailedChecks == 0 || negative.ItemSubtotalChecked {
		t.Fatalf("negative item should fail without a subtotal check: %+v", negative)
	}
	known, noAdjustments := true, false
	large := Reconcile(&llm.ExtractedReceipt{SubtotalCents: math.MaxInt64, TotalPaidCents: 1, TipKnown: &known, HasNonTaxAdjustments: &noAdjustments})
	if large.TaxSource == TaxSourceTotalInferred {
		t.Fatalf("underflowed residual was inferred as tax: %+v", large)
	}
}

func ptr(v int64) *int64 { return &v }

package httpapi

import (
	"errors"
	"testing"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
)

func TestBuildAttemptTelemetryAccountsKnownAndUnknownCosts(t *testing.T) {
	one := 1
	matched, unmatched := true, false
	zero, diff := int64(0), int64(300)
	rows, accounted, known, err := buildAttemptTelemetry([]extraction.Attempt{
		{
			Provider: "provider", Model: "model", CostCents: &one,
			SubtotalMatched: &unmatched, SubtotalDiffCents: &diff,
		},
		{
			Provider: "provider", Model: "model", Err: errors.New("failed"),
			SubtotalMatched: &matched, SubtotalDiffCents: &zero,
		},
	}, 2)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry: %v", err)
	}
	if accounted != 3 {
		t.Fatalf("accounted cost = %d, want 3", accounted)
	}
	if known != nil {
		t.Fatalf("known actual cost = %v, want nil", known)
	}
	if len(rows) != 2 || rows[0].SubtotalMatched == nil || *rows[0].SubtotalMatched {
		t.Fatalf("first attempt verification not preserved: %+v", rows)
	}
	if rows[1].Status != "error" || rows[1].CostCents != nil {
		t.Fatalf("failed attempt telemetry = %+v", rows[1])
	}
}

func TestNormalizeTaxableDefaultsOmittedInputToTrue(t *testing.T) {
	if !normalizeTaxable(nil) {
		t.Fatal("omitted taxable should default true")
	}
	no := false
	if normalizeTaxable(&no) {
		t.Fatal("explicit false should remain false")
	}
}

func TestExtractionResponseIncludesReconciliation(t *testing.T) {
	tax := int64(125)
	result := extraction.RunResult{Receipt: llm.ExtractedReceipt{Items: []llm.ExtractedItem{{Name: "coffee"}}}, Reconciliation: extraction.Reconciliation{
		ResolvedTaxCents: &tax, TaxSource: extraction.TaxSourceRateInferred,
		TaxChecked: true, TaxMatched: true, GrandTotalChecked: true, GrandTotalMatched: false, GrandTotalDiffCents: 2,
	}}
	got := extractionResponse(result)
	if got.TaxCents == nil || *got.TaxCents != 125 || got.Verification.TaxSource != "rate_inferred" {
		t.Fatalf("response tax mapping = %+v", got)
	}
	if !got.Verification.Tax.Checked || got.Verification.GrandTotal.Matched || got.Verification.GrandTotal.DiffCents != 2 {
		t.Fatalf("response verification = %+v", got.Verification)
	}
	if got.Items[0].Taxable == nil || !*got.Items[0].Taxable {
		t.Fatal("omitted item taxable marker should default true in response")
	}
}

func TestTelemetryTaxFieldsPreserveUncheckedAsNull(t *testing.T) {
	rows, _, _, err := buildAttemptTelemetry([]extraction.Attempt{{
		Provider: "provider", Model: "model",
		Reconciliation: extraction.Reconciliation{TaxMatched: false, TaxDiffCents: 7, GrandTotalMatched: false, GrandTotalDiffCents: 9},
	}}, 1)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry: %v", err)
	}
	if rows[0].TaxMatched != nil || rows[0].TaxDiffCents != nil || rows[0].GrandTotalMatched != nil || rows[0].GrandTotalDiffCents != nil {
		t.Fatalf("unchecked tax telemetry should be NULL: %+v", rows[0])
	}

	checked := extraction.Reconciliation{TaxChecked: true, TaxMatched: false, TaxDiffCents: 7, GrandTotalChecked: true, GrandTotalMatched: true}
	rows, _, _, err = buildAttemptTelemetry([]extraction.Attempt{{Provider: "provider", Model: "model", Reconciliation: checked}}, 1)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry checked: %v", err)
	}
	if rows[0].TaxMatched == nil || *rows[0].TaxMatched || rows[0].TaxDiffCents == nil || *rows[0].TaxDiffCents != 7 || rows[0].GrandTotalMatched == nil || !*rows[0].GrandTotalMatched {
		t.Fatalf("checked tax telemetry not preserved: %+v", rows[0])
	}
}

func TestTelemetryMultipleTaxRatesPreservesExplicitFalse(t *testing.T) {
	falseValue := false
	receipt := llm.ExtractedReceipt{MultipleTaxRatesDetected: &falseValue}
	reconciliation := extraction.Reconcile(&receipt)
	rows, _, _, err := buildAttemptTelemetry([]extraction.Attempt{{
		Provider: "provider", Model: "model", Reconciliation: reconciliation,
	}}, 1)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry: %v", err)
	}
	if rows[0].MultipleTaxRatesDetected == nil || *rows[0].MultipleTaxRatesDetected {
		t.Fatalf("explicit false multiple-rate telemetry was not preserved: %+v", rows[0])
	}

	unknown := extraction.Reconcile(&llm.ExtractedReceipt{})
	rows, _, _, err = buildAttemptTelemetry([]extraction.Attempt{{Provider: "provider", Model: "model", Reconciliation: unknown}}, 1)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry unknown: %v", err)
	}
	if rows[0].MultipleTaxRatesDetected != nil {
		t.Fatalf("unknown multiple-rate telemetry should be NULL: %+v", rows[0])
	}
}

func TestBuildAttemptTelemetryReleasesUnmadeCalls(t *testing.T) {
	one := 1
	_, accounted, known, err := buildAttemptTelemetry([]extraction.Attempt{{
		Provider: "provider", Model: "model", CostCents: &one,
	}}, 2)
	if err != nil {
		t.Fatalf("buildAttemptTelemetry: %v", err)
	}
	if accounted != 1 || known == nil || *known != 1 {
		t.Fatalf("accounted/known = %d/%v, want 1/1", accounted, known)
	}
}

func TestBuildAttemptTelemetryRejectsTooManyCalls(t *testing.T) {
	cost := 0
	attempts := []extraction.Attempt{
		{Provider: "provider", Model: "model", CostCents: &cost},
		{Provider: "provider", Model: "model", CostCents: &cost},
	}
	_, _, _, err := buildAttemptTelemetry(attempts, 1)
	if err == nil {
		t.Fatal("expected MaxCalls contract error")
	}
}

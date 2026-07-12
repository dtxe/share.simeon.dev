package httpapi

import (
	"errors"
	"testing"

	"share/backend/internal/extraction"
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

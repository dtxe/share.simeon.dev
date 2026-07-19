// Package feedback implements extraction strategy 02 from
// docs/plans/02-strategy-feedback-retry.md: the first call is today's
// baseline pipeline unchanged (same prompt/schema/thinking budget). If any
// verifiable receipt invariant doesn't reconcile, a
// second call replays the first attempt as real conversation history and
// points the model at the specific arithmetic discrepancy, asking it to
// re-examine the image and correct it. MaxCalls() == 2 — the second call
// only fires when the first one's subtotal check fails, so cost on the
// common (already-correct) case matches baseline.
package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

type Strategy struct {
	Client                     *openaicompat.Client
	ProviderName               string
	ModelName                  string
	InputCostPer1KTokensCents  float64
	OutputCostPer1KTokensCents float64
}

func New(client *openaicompat.Client, providerName, modelName string, inputCostPer1KTokensCents, outputCostPer1KTokensCents float64) *Strategy {
	return &Strategy{
		Client:                     client,
		ProviderName:               providerName,
		ModelName:                  modelName,
		InputCostPer1KTokensCents:  inputCostPer1KTokensCents,
		OutputCostPer1KTokensCents: outputCostPer1KTokensCents,
	}
}

func (s *Strategy) Name() string { return "feedback_retry" }

func (s *Strategy) MaxCalls() int { return 2 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	first, err := s.Client.ExtractReceiptAttempt(ctx, image, mimeType)
	if err != nil {
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.ProviderName,
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	rec1 := extraction.Reconcile(&first.Receipt)
	firstAttempt := s.buildAttempt(first.Receipt, first.Usage, rec1)

	if rec1.FailedChecks == 0 {
		return extraction.RunResult{
			Receipt:           first.Receipt,
			Attempts:          []extraction.Attempt{firstAttempt},
			SubtotalMatched:   subtotalMatchedPtr(rec1),
			SubtotalDiffCents: subtotalDiffPtr(rec1),
			Reconciliation:    rec1,
		}, nil
	}

	feedbackText := mismatchFeedback(rec1, first.Receipt)
	second, err := s.Client.ExtractReceiptFeedback(ctx, image, mimeType, first.ToolCallID, first.RawArguments, feedbackText)
	if err != nil {
		// The first attempt is still a usable, if unreconciled, result — a
		// retry-call failure (network blip, upstream error) shouldn't throw
		// away a receipt we already have.
		return extraction.RunResult{
			Receipt: first.Receipt,
			Attempts: []extraction.Attempt{firstAttempt, {
				Provider: s.ProviderName,
				Model:    s.ModelName,
				Err:      err,
			}},
			SubtotalMatched:   subtotalMatchedPtr(rec1),
			SubtotalDiffCents: subtotalDiffPtr(rec1),
			Reconciliation:    rec1,
		}, nil
	}

	rec2 := extraction.Reconcile(&second.Receipt)
	secondAttempt := s.buildAttempt(second.Receipt, second.Usage, rec2)
	// Preserve the first attempt unless the retry is strictly better. In
	// particular, an exact score tie must not replace the original extraction.
	chosen, chosenRec := first.Receipt, rec1
	if better(rec2, rec1) {
		chosen, chosenRec = second.Receipt, rec2
	}

	return extraction.RunResult{
		Receipt:           chosen,
		Attempts:          []extraction.Attempt{firstAttempt, secondAttempt},
		SubtotalMatched:   subtotalMatchedPtr(chosenRec),
		SubtotalDiffCents: subtotalDiffPtr(chosenRec),
		Reconciliation:    chosenRec,
	}, nil
}

func (s *Strategy) buildAttempt(receipt llm.ExtractedReceipt, usage llm.Usage, rec extraction.Reconciliation) extraction.Attempt {
	costCents := extraction.EstimateCostCents(usage.PromptTokens, usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
	rawJSON, _ := json.Marshal(receipt)
	return extraction.Attempt{
		Provider:          s.ProviderName,
		Model:             s.ModelName,
		PromptTok:         usage.PromptTokens,
		CompleteTok:       usage.CompletionTokens,
		CostCents:         &costCents,
		RawJSON:           rawJSON,
		SubtotalMatched:   subtotalMatchedPtr(rec),
		SubtotalDiffCents: subtotalDiffPtr(rec),
		Reconciliation:    rec,
	}
}

func subtotalMatchedPtr(r extraction.Reconciliation) *bool {
	if !r.ItemSubtotalChecked {
		return nil
	}
	v := r.ItemSubtotalMatched
	return &v
}
func subtotalDiffPtr(r extraction.Reconciliation) *int64 {
	if !r.ItemSubtotalChecked {
		return nil
	}
	v := r.ItemSubtotalDiffCents
	return &v
}

func better(a, b extraction.Reconciliation) bool {
	if a.FailedChecks != b.FailedChecks {
		return a.FailedChecks < b.FailedChecks
	}
	return a.AggregateAbsDifferenceCents < b.AggregateAbsDifferenceCents
}

func mismatchFeedback(rec extraction.Reconciliation, receipt llm.ExtractedReceipt) string {
	lines := []string{"Re-examine the receipt image and return corrected values. Verifiable discrepancies:"}
	if rec.ItemSubtotalChecked && !rec.ItemSubtotalMatched {
		lines = append(lines, fmt.Sprintf("items sum to %s but printed subtotal is %s", formatCents(rec.ItemSumCents), formatCents(receipt.SubtotalCents)))
	}
	if rec.TaxChecked && rec.ResolvedTaxCents != nil && !rec.TaxMatched && receipt.TaxRateBasisPoints != nil {
		lines = append(lines, fmt.Sprintf("tax differs from the printed rate by %s", formatCents(rec.TaxDiffCents)))
	}
	if receipt.TaxCents != nil && *receipt.TaxCents < 0 {
		lines = append(lines, "printed tax amount is negative")
	}
	if rec.GrandTotalChecked && !rec.GrandTotalMatched {
		lines = append(lines, fmt.Sprintf("subtotal plus resolved tax and tip differs from total by %s", formatCents(rec.GrandTotalDiffCents)))
	}
	return strings.Join(lines, "; ")
}

func formatCents(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", neg, cents/100, cents%100)
}

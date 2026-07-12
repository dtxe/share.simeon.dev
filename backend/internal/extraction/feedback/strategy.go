// Package feedback implements extraction strategy 02 from
// docs/plans/02-strategy-feedback-retry.md: the first call is today's
// baseline pipeline unchanged (same prompt/schema/thinking budget). If the
// extracted subtotal doesn't reconcile against the sum of its items, a
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

	matched, diffCents := extraction.CheckSubtotal(first.Receipt.Items, first.Receipt.SubtotalCents)
	firstAttempt := s.buildAttempt(first.Receipt, first.Usage, matched, diffCents)

	if matched {
		return extraction.RunResult{
			Receipt:           first.Receipt,
			Attempts:          []extraction.Attempt{firstAttempt},
			SubtotalMatched:   &matched,
			SubtotalDiffCents: &diffCents,
		}, nil
	}

	feedbackText := mismatchFeedback(extraction.SumItemsCents(first.Receipt.Items), first.Receipt.SubtotalCents)
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
			SubtotalMatched:   &matched,
			SubtotalDiffCents: &diffCents,
		}, nil
	}

	matched2, diffCents2 := extraction.CheckSubtotal(second.Receipt.Items, second.Receipt.SubtotalCents)
	secondAttempt := s.buildAttempt(second.Receipt, second.Usage, matched2, diffCents2)

	return extraction.RunResult{
		Receipt:           second.Receipt,
		Attempts:          []extraction.Attempt{firstAttempt, secondAttempt},
		SubtotalMatched:   &matched2,
		SubtotalDiffCents: &diffCents2,
	}, nil
}

func (s *Strategy) buildAttempt(receipt llm.ExtractedReceipt, usage llm.Usage, matched bool, diffCents int64) extraction.Attempt {
	costCents := extraction.EstimateCostCents(usage.PromptTokens, usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
	rawJSON, _ := json.Marshal(receipt)
	return extraction.Attempt{
		Provider:          s.ProviderName,
		Model:             s.ModelName,
		PromptTok:         usage.PromptTokens,
		CompleteTok:       usage.CompletionTokens,
		CostCents:         &costCents,
		RawJSON:           rawJSON,
		SubtotalMatched:   &matched,
		SubtotalDiffCents: &diffCents,
	}
}

func mismatchFeedback(computedCents, subtotalCents int64) string {
	return fmt.Sprintf(
		"Your extraction says the items sum to %s but the receipt's printed subtotal is %s — re-examine the image, "+
			"the most likely error is treating a line-total as a per-unit price on one or more items. Return corrected values.",
		formatCents(computedCents), formatCents(subtotalCents))
}

func formatCents(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", neg, cents/100, cents%100)
}

// Package feedback implements extraction strategy 02 from
// docs/plans/02-strategy-feedback-retry.md: the first call is today's
// baseline pipeline unchanged (same prompt/schema/thinking budget). If the
// extracted subtotal doesn't reconcile against the sum of its items, a
// second operation replays the first attempt as real conversation history and
// points the model at the specific arithmetic discrepancy, asking it to
// re-examine the image and correct it. MaxCalls() == 4 — each operation can
// make an initial and calculator-final call; the second operation
// only fires when the first one's subtotal check fails, so cost on the
// common (already-correct) case matches baseline.
package feedback

import (
	"context"
	"errors"
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

func (s *Strategy) MaxCalls() int { return 4 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	first, err := s.Client.ExtractReceiptAttempt(ctx, image, mimeType)
	if err != nil {
		if calls := responseAttempts(err); len(calls) > 0 {
			return extraction.RunResult{Attempts: s.buildCallAttempts(calls, err)}, err
		}
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.ProviderName,
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	matched, diffCents := extraction.CheckSubtotal(first.Receipt.Items, first.Receipt.SubtotalCents)
	firstAttempts := s.buildCallAttempts(first.Attempts, nil)
	if len(firstAttempts) == 0 {
		firstAttempts = []extraction.Attempt{s.buildAttempt(first.RawResponse, first.Usage, matched, diffCents)}
	}
	firstAttempts[len(firstAttempts)-1].SubtotalMatched = &matched
	firstAttempts[len(firstAttempts)-1].SubtotalDiffCents = &diffCents

	if matched {
		return extraction.RunResult{
			Receipt:           first.Receipt,
			Attempts:          firstAttempts,
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
		secondAttempts := s.buildCallAttempts(responseAttempts(err), err)
		if len(secondAttempts) == 0 {
			secondAttempts = []extraction.Attempt{{Provider: s.ProviderName, Model: s.ModelName, Err: err}}
		}
		return extraction.RunResult{
			Receipt:           first.Receipt,
			Attempts:          append(firstAttempts, secondAttempts...),
			SubtotalMatched:   &matched,
			SubtotalDiffCents: &diffCents,
		}, nil
	}

	matched2, diffCents2 := extraction.CheckSubtotal(second.Receipt.Items, second.Receipt.SubtotalCents)
	secondAttempts := s.buildCallAttempts(second.Attempts, nil)
	if len(secondAttempts) == 0 {
		secondAttempts = []extraction.Attempt{s.buildAttempt(second.RawResponse, second.Usage, matched2, diffCents2)}
	}
	secondAttempts[len(secondAttempts)-1].SubtotalMatched = &matched2
	secondAttempts[len(secondAttempts)-1].SubtotalDiffCents = &diffCents2

	return extraction.RunResult{
		Receipt:           second.Receipt,
		Attempts:          append(firstAttempts, secondAttempts...),
		SubtotalMatched:   &matched2,
		SubtotalDiffCents: &diffCents2,
	}, nil
}

func (s *Strategy) buildAttempt(raw []byte, usage llm.Usage, matched bool, diffCents int64) extraction.Attempt {
	costCents := extraction.EstimateCostCents(usage.PromptTokens, usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
	return extraction.Attempt{
		Provider:          s.ProviderName,
		Model:             s.ModelName,
		PromptTok:         usage.PromptTokens,
		CompleteTok:       usage.CompletionTokens,
		CostCents:         &costCents,
		RawJSON:           raw,
		SubtotalMatched:   &matched,
		SubtotalDiffCents: &diffCents,
	}
}

func responseAttempts(err error) []llm.Attempt {
	var responseErr *llm.ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.Attempts
	}
	return nil
}

func (s *Strategy) buildCallAttempts(calls []llm.Attempt, err error) []extraction.Attempt {
	result := make([]extraction.Attempt, 0, len(calls))
	for i, call := range calls {
		callErr := error(nil)
		if i == len(calls)-1 {
			callErr = err
		}
		var cost *int
		if callErr == nil || call.Usage.PromptTokens > 0 || call.Usage.CompletionTokens > 0 {
			value := extraction.EstimateCostCents(call.Usage.PromptTokens, call.Usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
			cost = &value
		}
		result = append(result, extraction.Attempt{Provider: s.ProviderName, Model: s.ModelName, PromptTok: call.Usage.PromptTokens, CompleteTok: call.Usage.CompletionTokens, CostCents: cost, RawJSON: call.RawResponse, Err: callErr})
	}
	return result
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

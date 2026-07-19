// Package baseline is the strategy-shaped skin around today's extraction
// behavior: one llm.Provider.ExtractReceipt call, subtotal check reported
// via the shared extraction.CheckSubtotal helper. It changes zero behavior
// versus the pre-strategy code path — it exists to prove the
// extraction.Strategy abstraction before any real new strategy is built.
package baseline

import (
	"context"
	"encoding/json"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
)

type Strategy struct {
	LLM                        llm.Provider
	ModelName                  string
	InputCostPer1KTokensCents  float64
	OutputCostPer1KTokensCents float64
}

func New(provider llm.Provider, modelName string, inputCostPer1KTokensCents, outputCostPer1KTokensCents float64) *Strategy {
	return &Strategy{
		LLM:                        provider,
		ModelName:                  modelName,
		InputCostPer1KTokensCents:  inputCostPer1KTokensCents,
		OutputCostPer1KTokensCents: outputCostPer1KTokensCents,
	}
}

func (s *Strategy) Name() string { return "baseline" }

func (s *Strategy) MaxCalls() int { return 1 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	result, err := s.LLM.ExtractReceipt(ctx, image, mimeType)
	if err != nil {
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.LLM.Name(),
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	costCents := extraction.EstimateCostCents(result.Usage.PromptTokens, result.Usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
	rawJSON, _ := json.Marshal(result.Receipt)

	reconciliation := extraction.Reconcile(&result.Receipt)

	return extraction.RunResult{
		Receipt: result.Receipt,
		Attempts: []extraction.Attempt{
			{
				Provider:          s.LLM.Name(),
				Model:             s.ModelName,
				PromptTok:         result.Usage.PromptTokens,
				CompleteTok:       result.Usage.CompletionTokens,
				CostCents:         &costCents,
				RawJSON:           rawJSON,
				SubtotalMatched:   subtotalMatchedPtr(reconciliation),
				SubtotalDiffCents: subtotalDiffPtr(reconciliation),
				Reconciliation:    reconciliation,
			},
		},
		SubtotalMatched:   subtotalMatchedPtr(reconciliation),
		SubtotalDiffCents: subtotalDiffPtr(reconciliation),
		Reconciliation:    reconciliation,
	}, nil
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

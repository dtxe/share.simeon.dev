// Package baseline is the strategy-shaped skin around today's extraction
// behavior: one llm.Provider.ExtractReceipt operation (which may use two
// provider turns for local calculator verification), subtotal check reported
// via the shared extraction.CheckSubtotal helper. It changes zero behavior
// versus the pre-strategy code path — it exists to prove the
// extraction.Strategy abstraction before any real new strategy is built.
package baseline

import (
	"context"
	"encoding/json"
	"errors"

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

func (s *Strategy) MaxCalls() int { return 2 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	result, err := s.LLM.ExtractReceipt(ctx, image, mimeType)
	if err != nil {
		var responseErr *llm.ResponseError
		if errors.As(err, &responseErr) && len(responseErr.Attempts) > 0 {
			attempts := make([]extraction.Attempt, 0, len(responseErr.Attempts))
			for i, call := range responseErr.Attempts {
				var callErr error
				if i == len(responseErr.Attempts)-1 {
					callErr = err
				}
				attempts = append(attempts, s.buildCallAttempt(call, callErr))
			}
			return extraction.RunResult{Attempts: attempts}, err
		}
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.LLM.Name(),
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	matched, diffCents := extraction.CheckSubtotal(result.Receipt.Items, result.Receipt.SubtotalCents)
	callResults := result.Attempts
	if len(callResults) == 0 {
		raw := result.RawResponse
		if len(raw) == 0 { // legacy providers do not expose upstream bytes.
			raw, _ = json.Marshal(result.Receipt)
		}
		callResults = []llm.Attempt{{RawResponse: raw, Usage: result.Usage}}
	}
	attempts := make([]extraction.Attempt, 0, len(callResults))
	for i, call := range callResults {
		var callErr error
		if i == len(callResults)-1 {
			callErr = err
		}
		attempt := s.buildCallAttempt(call, callErr)
		if i == len(callResults)-1 {
			attempt.SubtotalMatched = &matched
			attempt.SubtotalDiffCents = &diffCents
		}
		attempts = append(attempts, attempt)
	}

	return extraction.RunResult{
		Receipt:           result.Receipt,
		Attempts:          attempts,
		SubtotalMatched:   &matched,
		SubtotalDiffCents: &diffCents,
	}, nil
}

func (s *Strategy) buildCallAttempt(call llm.Attempt, err error) extraction.Attempt {
	var cost *int
	if err == nil || call.Usage.PromptTokens > 0 || call.Usage.CompletionTokens > 0 {
		value := extraction.EstimateCostCents(call.Usage.PromptTokens, call.Usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
		cost = &value
	}
	return extraction.Attempt{Provider: s.LLM.Name(), Model: s.ModelName, PromptTok: call.Usage.PromptTokens, CompleteTok: call.Usage.CompletionTokens, CostCents: cost, RawJSON: call.RawResponse, Err: err}
}

package deterministic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

// Strategy is extraction 01: one vision-LLM call with a schema/prompt that
// forbids self-correction, then a pure Go pass (ResolvePriceFormat) that
// resolves the price-format ambiguity deterministically. MaxCalls() == 1 —
// same call-count/cost profile as baseline.
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

func (s *Strategy) Name() string { return "deterministic_check" }

func (s *Strategy) MaxCalls() int { return 1 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	prompt := extractionPrompt
	thinkingBudget := 0
	if openaicompat.SupportsThinking(s.ModelName) {
		thinkingBudget = openaicompat.MinThinkingBudgetTokens
	} else {
		prompt += "\n\n" + openaicompat.MinimizeReasoningPromptSuffix
	}

	raw, usage, err := s.Client.ExtractWithSchema(ctx, image, mimeType, prompt, extractionSchema, thinkingBudget)
	if err != nil {
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.ProviderName,
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	var pr printedReceipt
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pr); err != nil {
		wrapped := fmt.Errorf("deterministic: decoding extracted receipt tool call arguments: %w (raw arguments: %s)", err, raw)
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider:    s.ProviderName,
				Model:       s.ModelName,
				PromptTok:   usage.PromptTokens,
				CompleteTok: usage.CompletionTokens,
				Err:         wrapped,
			}},
		}, wrapped
	}

	resolvedItems, matched, diffCents := ResolvePriceFormat(pr.Items, pr.SubtotalCents)
	receipt := llm.ExtractedReceipt{
		RestaurantName: pr.RestaurantName,
		Date:           pr.Date,
		SubtotalCents:  pr.SubtotalCents,
		TipCents:       pr.TipCents,
		TotalPaidCents: pr.TotalPaidCents,
		Items:          resolvedItems,
	}
	rawJSON, _ := json.Marshal(receipt)
	costCents := extraction.EstimateCostCents(usage.PromptTokens, usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)

	return extraction.RunResult{
		Receipt: receipt,
		Attempts: []extraction.Attempt{
			{
				Provider:          s.ProviderName,
				Model:             s.ModelName,
				PromptTok:         usage.PromptTokens,
				CompleteTok:       usage.CompletionTokens,
				CostCents:         &costCents,
				RawJSON:           rawJSON,
				SubtotalMatched:   &matched,
				SubtotalDiffCents: &diffCents,
			},
		},
		SubtotalMatched:   &matched,
		SubtotalDiffCents: &diffCents,
	}, nil
}

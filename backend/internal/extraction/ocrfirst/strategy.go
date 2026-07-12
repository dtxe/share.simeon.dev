// Package ocrfirst implements extraction strategy 03 from
// docs/plans/03-strategy-ocr-first.md: a local OCR pass (internal/ocr, no
// LLM call, no added spend) transcribes raw text from the receipt image,
// then a single text-only chat completion structures that text into the
// same extract_receipt schema shape as baseline. MaxCalls() == 1 — the OCR
// pass isn't an LLM call, so cost/reservation sizing matches baseline
// exactly; the only added cost is non-LLM subprocess latency.
package ocrfirst

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"share/backend/internal/extraction"
	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

// Extractor is the OCR engine dependency, satisfied by *internal/ocr.Engine.
// A narrow interface here keeps the strategy testable without shelling out
// to a real tesseract binary.
type Extractor interface {
	Extract(ctx context.Context, image []byte) (string, error)
}

type Strategy struct {
	OCR                        Extractor
	Client                     *openaicompat.Client
	ProviderName               string
	ModelName                  string
	InputCostPer1KTokensCents  float64
	OutputCostPer1KTokensCents float64
}

func New(ocrEngine Extractor, client *openaicompat.Client, providerName, modelName string, inputCostPer1KTokensCents, outputCostPer1KTokensCents float64) *Strategy {
	return &Strategy{
		OCR:                        ocrEngine,
		Client:                     client,
		ProviderName:               providerName,
		ModelName:                  modelName,
		InputCostPer1KTokensCents:  inputCostPer1KTokensCents,
		OutputCostPer1KTokensCents: outputCostPer1KTokensCents,
	}
}

func (s *Strategy) Name() string { return "ocr_first" }

func (s *Strategy) MaxCalls() int { return 1 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	text, err := s.OCR.Extract(ctx, image)
	if err != nil {
		return extraction.RunResult{}, fmt.Errorf("ocr_first: ocr extraction: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return extraction.RunResult{}, fmt.Errorf("ocr_first: ocr produced no text")
	}

	thinkingBudget := 0
	prompt := structuringPrompt + text
	if openaicompat.SupportsThinking(s.ModelName) {
		thinkingBudget = openaicompat.MinThinkingBudgetTokens
	} else {
		prompt += "\n\n" + openaicompat.MinimizeReasoningPromptSuffix
	}

	raw, usage, err := s.Client.ExtractFromText(ctx, prompt, extractionSchema, thinkingBudget)
	if err != nil {
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider: s.ProviderName,
				Model:    s.ModelName,
				Err:      err,
			}},
		}, err
	}

	receipt, err := decodeReceipt(raw)
	if err != nil {
		return extraction.RunResult{
			Attempts: []extraction.Attempt{{
				Provider:    s.ProviderName,
				Model:       s.ModelName,
				PromptTok:   usage.PromptTokens,
				CompleteTok: usage.CompletionTokens,
				Err:         err,
			}},
		}, err
	}

	costCents := extraction.EstimateCostCents(usage.PromptTokens, usage.CompletionTokens, s.InputCostPer1KTokensCents, s.OutputCostPer1KTokensCents)
	rawJSON, _ := json.Marshal(receipt)
	matched, diffCents := extraction.CheckSubtotal(receipt.Items, receipt.SubtotalCents)

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

func decodeReceipt(raw json.RawMessage) (llm.ExtractedReceipt, error) {
	var receipt llm.ExtractedReceipt
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return llm.ExtractedReceipt{}, fmt.Errorf("ocr_first: decoding extracted receipt tool call arguments: %w (raw arguments: %s)", err, raw)
	}
	return receipt, nil
}

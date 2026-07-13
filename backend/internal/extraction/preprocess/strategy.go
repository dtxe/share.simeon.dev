// Package preprocess applies deterministic image cleanup before the baseline
// vision extraction call.
package preprocess

import (
	"context"

	"share/backend/internal/extraction"
	"share/backend/internal/extraction/baseline"
	"share/backend/internal/imageprep"
	"share/backend/internal/llm"
)

type Strategy struct {
	baseline *baseline.Strategy
}

func New(provider llm.Provider, modelName string, inputCostPer1KTokensCents, outputCostPer1KTokensCents float64) *Strategy {
	return &Strategy{
		baseline: baseline.New(provider, modelName, inputCostPer1KTokensCents, outputCostPer1KTokensCents),
	}
}

func (s *Strategy) Name() string { return "image_preprocess" }

func (s *Strategy) MaxCalls() int { return 1 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	cleaned, err := imageprep.Clean(image)
	if err != nil {
		return extraction.RunResult{}, err
	}
	return s.baseline.Run(ctx, cleaned, mimeType)
}

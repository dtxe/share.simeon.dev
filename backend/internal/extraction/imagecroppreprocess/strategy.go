// Package imagecroppreprocess implements a crop-then-extract strategy
// (image_crop_preprocess) for benchmark use only. It makes a VLM crop-detection
// call (detect_receipt_boundary), crops the image to the detected receipt
// bounds, applies Clean (binarization), then delegates to the baseline
// extraction strategy. If crop detection or cropping fails, it falls back
// to phase-1: one Clean + baseline extraction call.
// MaxCalls() == 2 (crop detection + extraction; fallback path uses 1).
package imagecroppreprocess

import (
	"context"
	"encoding/json"
	"fmt"

	"share/backend/internal/extraction"
	"share/backend/internal/extraction/baseline"
	"share/backend/internal/extraction/cropdetect"
	"share/backend/internal/imageprep"
	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

// CropDetector is the interface the strategy uses to detect receipt bounds.
// The real implementation is cropdetect.Detect; the interface exists so tests
// can inject a fake detector without making real LLM calls.
type CropDetector interface {
	Detect(ctx context.Context, image []byte, mimeType string) (*cropdetect.DetectionResult, llm.Usage, error)
}

// RealDetector wraps cropdetect.Detect with an openaicompat client.
type RealDetector struct {
	Client         *openaicompat.Client
	Model          string
	ThinkingBudget int
}

func (d *RealDetector) Detect(ctx context.Context, image []byte, mimeType string) (*cropdetect.DetectionResult, llm.Usage, error) {
	return cropdetect.Detect(ctx, d.Client, d.Model, image, mimeType, d.ThinkingBudget)
}

type Strategy struct {
	detector                       CropDetector
	cropLLMProviderName            string
	cropLLMModelName               string
	cropInputCostPer1KTokensCents  float64
	cropOutputCostPer1KTokensCents float64
	baseline                       *baseline.Strategy
}

// New creates an image_crop_preprocess strategy. The cropDetector should
// be a realDetector wrapping an openaicompat.Client for bench use (or a test
// double). The baseline provider is used for the extraction step.
func New(
	detector CropDetector,
	cropProviderName, cropModelName string,
	cropInputCostPer1KTokensCents, cropOutputCostPer1KTokensCents float64,
	baselineProvider llm.Provider,
	baselineModelName string,
	baselineInputCostPer1KTokensCents, baselineOutputCostPer1KTokensCents float64,
) *Strategy {
	return &Strategy{
		detector:                       detector,
		cropLLMProviderName:            cropProviderName,
		cropLLMModelName:               cropModelName,
		cropInputCostPer1KTokensCents:  cropInputCostPer1KTokensCents,
		cropOutputCostPer1KTokensCents: cropOutputCostPer1KTokensCents,
		baseline:                       baseline.New(baselineProvider, baselineModelName, baselineInputCostPer1KTokensCents, baselineOutputCostPer1KTokensCents),
	}
}

func (s *Strategy) Name() string { return "image_crop_preprocess" }

func (s *Strategy) MaxCalls() int { return 2 }

func (s *Strategy) Run(ctx context.Context, image []byte, mimeType string) (extraction.RunResult, error) {
	// Step 1: Crop detection call.
	detection, cropUsage, err := s.detector.Detect(ctx, image, mimeType)
	if err != nil {
		// The detection LLM call was actually initiated (we got usage or actual
		// error from the provider), so report it as an Attempt.
		cropAttempt := extraction.Attempt{
			Provider:    s.cropLLMProviderName,
			Model:       s.cropLLMModelName,
			PromptTok:   cropUsage.PromptTokens,
			CompleteTok: cropUsage.CompletionTokens,
			Err:         fmt.Errorf("image_crop_preprocess: crop detection: %w", err),
		}
		if cropUsage.PromptTokens > 0 || cropUsage.CompletionTokens > 0 {
			costCents := extraction.EstimateCostCents(cropUsage.PromptTokens, cropUsage.CompletionTokens, s.cropInputCostPer1KTokensCents, s.cropOutputCostPer1KTokensCents)
			cropAttempt.CostCents = &costCents
		}

		// Fall back to phase-1: Clean + baseline extraction (one call).
		return s.fallbackWithAttempt(ctx, image, mimeType, cropAttempt)
	}

	// Crop detection succeeded. Calculate cost for the crop detection attempt.
	cropCostCents := extraction.EstimateCostCents(cropUsage.PromptTokens, cropUsage.CompletionTokens, s.cropInputCostPer1KTokensCents, s.cropOutputCostPer1KTokensCents)
	cropAttempt := extraction.Attempt{
		Provider:    s.cropLLMProviderName,
		Model:       s.cropLLMModelName,
		PromptTok:   cropUsage.PromptTokens,
		CompleteTok: cropUsage.CompletionTokens,
		CostCents:   &cropCostCents,
		RawJSON:     mustMarshal(detection),
	}

	// Step 2: Apply crop.
	cropped, err := imageprep.Crop(image, imageprep.CropBounds{
		MinX: detection.Bounds.MinX,
		MinY: detection.Bounds.MinY,
		MaxX: detection.Bounds.MaxX,
		MaxY: detection.Bounds.MaxY,
	})
	if err != nil {
		// Crop operation failed (possible with valid but degenerate bounds).
		// Report the crop detection attempt and fall back to phase-1.
		cropAttempt.Err = fmt.Errorf("image_crop_preprocess: crop operation: %w", err)
		return s.fallbackWithAttempt(ctx, image, mimeType, cropAttempt)
	}

	// Step 3: Apply Clean to the cropped image.
	cleaned, err := imageprep.Clean(cropped)
	if err != nil {
		// Clean of cropped image failed. Fall back to phase-1 on the original.
		cropAttempt.Err = fmt.Errorf("image_crop_preprocess: clean after crop: %w", err)
		return s.fallbackWithAttempt(ctx, image, mimeType, cropAttempt)
	}

	// Step 4: Delegate to baseline on cleaned+cropped image.
	baselineResult, err := s.baseline.Run(ctx, cleaned, mimeType)
	if err != nil {
		// Baseline extraction failed. Report both attempts but still return
		// the baseline's RunResult (which carries the failed attempt).
		baselineResult.Attempts = append([]extraction.Attempt{cropAttempt}, baselineResult.Attempts...)
		return baselineResult, err
	}

	baselineResult.Attempts = append([]extraction.Attempt{cropAttempt}, baselineResult.Attempts...)
	return baselineResult, nil
}

// fallbackWithAttempt runs the phase-1 fallback (Clean + baseline) and
// prepends the given cropAttempt to the result's Attempts slice.
func (s *Strategy) fallbackWithAttempt(ctx context.Context, image []byte, mimeType string, cropAttempt extraction.Attempt) (extraction.RunResult, error) {
	cleaned, err := imageprep.Clean(image)
	if err != nil {
		// Even the phase-1 cleanup failed — propagate the crop attempt + empty result.
		return extraction.RunResult{
			Attempts: []extraction.Attempt{cropAttempt},
		}, fmt.Errorf("image_crop_preprocess: fallback clean: %w (crop error: %v)", err, cropAttempt.Err)
	}

	baselineResult, err := s.baseline.Run(ctx, cleaned, mimeType)
	if err != nil {
		baselineResult.Attempts = append([]extraction.Attempt{cropAttempt}, baselineResult.Attempts...)
		return baselineResult, err
	}

	baselineResult.Attempts = append([]extraction.Attempt{cropAttempt}, baselineResult.Attempts...)
	return baselineResult, nil
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

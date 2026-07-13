// Package cropdetect provides a VLM-based receipt-boundary detector that
// returns normalized crop coordinates (0..1000) via an OpenAI-wire-compatible
// forced tool call. Designed for use as the first step in a crop-then-extract
// pipeline (image_crop_preprocess strategy) — not for production server wiring.
package cropdetect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"

	"share/backend/internal/llm"
	"share/backend/internal/llm/openaicompat"
)

// DetectionFunctionName is the JSON schema tool name the model must call.
const DetectionFunctionName = "detect_receipt_boundary"

const detectionPrompt = `You are analyzing a photo of a receipt to find its rectangular boundary.
The receipt is the physical paper slip — ignore any background surface (table, desk, counter) it was photographed on.
Return the bounding rectangle that tightly encloses the receipt paper itself, expressed as normalized integer coordinates from 0 to 1000 where (0,0) is the top-left corner of the full image and (1000,1000) is the bottom-right corner.
- minX: left edge of the receipt
- minY: top edge of the receipt
- maxX: right edge of the receipt
- maxY: bottom edge of the receipt
The coordinates must satisfy 0 <= minX < maxX <= 1000 and 0 <= minY < maxY <= 1000.
If you are not confident about the exact boundary, set confidence to a value between 0.0 and 1.0 (1.0 = very confident).
If there is clearly no receipt in the image, return confidence = 0.0.
Call the detect_receipt_boundary function with the result — do not respond in plain text.`

// Bounds are normalized integer coordinates on a 0..1000 grid, mapping
// linearly to the original image's pixel dimensions. minX/minY are the
// top-left corner and maxX/maxY are the bottom-right corner (exclusive).
// All fields must satisfy 0 <= minX < maxX <= 1000 and 0 <= minY < maxY <= 1000.
type Bounds struct {
	MinX int `json:"minX"`
	MinY int `json:"minY"`
	MaxX int `json:"maxX"`
	MaxY int `json:"maxY"`
}

// DetectionResult is the structured output of a crop-detection LLM call.
// Confidence, when present, is the model's self-reported certainty (0.0–1.0).
// Unknown JSON fields cause a strict-decode error.
type DetectionResult struct {
	Bounds     Bounds   `json:"bounds"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// Validate checks that bounds are within the 0..1000 range, non-empty, and
// that confidence (if supplied) is a finite value in [0.0, 1.0].
func (d *DetectionResult) Validate() error {
	b := d.Bounds
	if b.MinX < 0 || b.MinY < 0 || b.MaxX > 1000 || b.MaxY > 1000 {
		return fmt.Errorf("cropdetect: bounds [%d,%d,%d,%d] outside 0..1000", b.MinX, b.MinY, b.MaxX, b.MaxY)
	}
	if b.MinX >= b.MaxX {
		return fmt.Errorf("cropdetect: minX (%d) >= maxX (%d)", b.MinX, b.MaxX)
	}
	if b.MinY >= b.MaxY {
		return fmt.Errorf("cropdetect: minY (%d) >= maxY (%d)", b.MinY, b.MaxY)
	}
	if d.Confidence != nil {
		c := *d.Confidence
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return fmt.Errorf("cropdetect: confidence is not a finite number")
		}
		if c < 0.0 || c > 1.0 {
			return fmt.Errorf("cropdetect: confidence %f outside [0.0, 1.0]", c)
		}
	}
	return nil
}

// detectionSchema is the JSON schema sent to the model as the tool definition.
var detectionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"bounds": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"minX": map[string]any{"type": "integer", "description": "left edge of the receipt, 0..1000"},
				"minY": map[string]any{"type": "integer", "description": "top edge of the receipt, 0..1000"},
				"maxX": map[string]any{"type": "integer", "description": "right edge of the receipt, 0..1000"},
				"maxY": map[string]any{"type": "integer", "description": "bottom edge of the receipt, 0..1000"},
			},
			"required": []string{"minX", "minY", "maxX", "maxY"},
		},
		"confidence": map[string]any{"type": "number", "description": "model confidence 0.0–1.0, omit if uncertain"},
	},
	"required": []string{"bounds"},
}

// cropDetectMaxTokens is the max_tokens sent to the crop-detection VLM. This
// is intentionally small — the model only needs to output a short structured
// JSON object (bounds + optional confidence), not a full receipt.
const cropDetectMaxTokens = 512

// Detect calls the LLM to detect receipt boundaries in the given image.
// Returns the parsed, validated DetectionResult. The thinking budget is
// determined from the model (0 if the model doesn't support thinking).
// Max_tokens for the call is fixed at 512.
func Detect(ctx context.Context, client *openaicompat.Client, model string, image []byte, mimeType string, thinkingBudgetTokens int) (*DetectionResult, llm.Usage, error) {
	prompt := detectionPrompt
	if !openaicompat.SupportsThinking(model) {
		prompt += "\n\n" + openaicompat.MinimizeReasoningPromptSuffix
	}

	raw, usage, err := client.ExtractStructuredCall(ctx, image, mimeType, prompt, DetectionFunctionName, detectionSchema, thinkingBudgetTokens, cropDetectMaxTokens, true)
	if err != nil {
		return nil, usage, fmt.Errorf("cropdetect: structured call: %w", err)
	}

	var result DetectionResult
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return nil, usage, fmt.Errorf("cropdetect: decoding detection result: %w (raw: %s)", err, raw)
	}

	if err := result.Validate(); err != nil {
		return nil, usage, fmt.Errorf("cropdetect: validation: %w (raw: %s)", err, raw)
	}

	return &result, usage, nil
}

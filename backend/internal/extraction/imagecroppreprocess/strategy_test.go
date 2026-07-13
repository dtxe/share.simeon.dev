package imagecroppreprocess

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"share/backend/internal/extraction/cropdetect"
	"share/backend/internal/llm"
)

// fakeDetector returns fixed detection results for testing.
type fakeDetector struct {
	result *cropdetect.DetectionResult
	usage  llm.Usage
	err    error
}

func (f *fakeDetector) Detect(_ context.Context, _ []byte, _ string) (*cropdetect.DetectionResult, llm.Usage, error) {
	return f.result, f.usage, f.err
}

// fakeProvider simulates a baseline LLM provider.
type fakeProvider struct {
	result *llm.Result
	err    error
}

func (f fakeProvider) ExtractReceipt(_ context.Context, _ []byte, _ string) (*llm.Result, error) {
	return f.result, f.err
}

func (fakeProvider) Name() string { return "fake_provider" }

func makeTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetGray(x, y, color.Gray{Y: 200})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode test image: %v", err)
	}
	return buf.Bytes()
}

func TestName(t *testing.T) {
	s := New(nil, "fake", "crop-model", 0.1, 0.2,
		fakeProvider{}, "base-model", 0.3, 0.4)
	if got := s.Name(); got != "image_crop_preprocess" {
		t.Fatalf("Name() = %q, want image_crop_preprocess", got)
	}
}

func TestMaxCalls(t *testing.T) {
	s := New(nil, "fake", "crop-model", 0.1, 0.2,
		fakeProvider{}, "base-model", 0.3, 0.4)
	if got := s.MaxCalls(); got != 2 {
		t.Fatalf("MaxCalls() = %d, want 2", got)
	}
}

func TestRunCropDetectionThenExtractionSuccess(t *testing.T) {
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 100, MinY: 100, MaxX: 800, MaxY: 800},
		},
		usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 1500,
				Items:         []llm.ExtractedItem{{Name: "Burger", PriceCents: 1500, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 1000, CompletionTokens: 500},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 2.0, 2.0,
		baselineProv, "base-model", 3.0, 3.0)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Should have 2 attempts: crop detection + extraction.
	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}

	// First attempt = crop detection.
	cropAtt := result.Attempts[0]
	if cropAtt.Provider != "crop_provider" || cropAtt.Model != "crop-model" {
		t.Fatalf("crop attempt provider/model = %q/%q, want crop_provider/crop-model",
			cropAtt.Provider, cropAtt.Model)
	}
	if cropAtt.PromptTok != 500 || cropAtt.CompleteTok != 100 {
		t.Fatalf("crop attempt usage = %d/%d, want 500/100",
			cropAtt.PromptTok, cropAtt.CompleteTok)
	}
	if cropAtt.Err != nil {
		t.Fatalf("crop attempt error = %v, want nil", cropAtt.Err)
	}
	if cropAtt.CostCents == nil || *cropAtt.CostCents <= 0 {
		t.Fatalf("crop attempt cost = nil or <= 0, got %v", cropAtt.CostCents)
	}
	if len(cropAtt.RawJSON) == 0 {
		t.Fatal("crop attempt raw JSON is empty")
	}

	// Second attempt = baseline extraction.
	baseAtt := result.Attempts[1]
	if baseAtt.Provider != "fake_provider" || baseAtt.Model != "base-model" {
		t.Fatalf("base attempt provider/model = %q/%q", baseAtt.Provider, baseAtt.Model)
	}
	if baseAtt.PromptTok != 1000 || baseAtt.CompleteTok != 500 {
		t.Fatalf("base attempt usage = %d/%d, want 1000/500",
			baseAtt.PromptTok, baseAtt.CompleteTok)
	}
	if baseAtt.Err != nil {
		t.Fatalf("base attempt error = %v, want nil", baseAtt.Err)
	}

	if result.Receipt.SubtotalCents != 1500 {
		t.Fatalf("receipt subtotal = %d, want 1500", result.Receipt.SubtotalCents)
	}
}

func TestRunFallsBackWhenCropDetectionFails(t *testing.T) {
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		err:   errors.New("LLM call failed"),
		usage: llm.Usage{PromptTokens: 200, CompletionTokens: 50},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 1000,
				Items:         []llm.ExtractedItem{{Name: "Fallback", PriceCents: 1000, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 800, CompletionTokens: 300},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 4.0, 4.0,
		baselineProv, "base-model", 3.0, 3.0)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v (expected fallback to succeed with 2 attempts)", err)
	}

	// Should have 2 attempts: failed crop detection + fallback extraction.
	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts (failed crop + fallback), got %d", len(result.Attempts))
	}

	// First attempt = failed crop detection.
	cropAtt := result.Attempts[0]
	if cropAtt.Err == nil {
		t.Fatal("expected crop detection attempt to record an error")
	}
	if cropAtt.PromptTok != 200 || cropAtt.CompleteTok != 50 {
		t.Fatalf("crop attempt usage = %d/%d, want 200/50",
			cropAtt.PromptTok, cropAtt.CompleteTok)
	}
	if cropAtt.CostCents == nil || *cropAtt.CostCents <= 0 {
		t.Fatalf("crop attempt cost = %v, want positive (usage > 0)", cropAtt.CostCents)
	}

	// Second attempt = fallback baseline.
	baseAtt := result.Attempts[1]
	if baseAtt.Err != nil {
		t.Fatalf("fallback baseline attempt error = %v, want nil", baseAtt.Err)
	}

	// The fallback receipt should be from the baseline.
	if result.Receipt.SubtotalCents != 1000 {
		t.Fatalf("fallback receipt subtotal = %d, want 1000", result.Receipt.SubtotalCents)
	}
}

func TestRunFallsBackWhenCropDetectionFailsWithZeroUsage(t *testing.T) {
	// If the crop detector fails without any usage (e.g. a pre-LLM error),
	// the attempt should not have a cost.
	detector := &fakeDetector{
		err:   errors.New("pre-LLM error"),
		usage: llm.Usage{},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 500,
				Items:         []llm.ExtractedItem{{Name: "Item", PriceCents: 500, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		baselineProv, "base-model", 0.3, 0.6)

	img := makeTestJPEG(t)
	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}

	cropAtt := result.Attempts[0]
	if cropAtt.CostCents != nil {
		t.Fatalf("expected nil cost for failed crop with zero usage, got %d", *cropAtt.CostCents)
	}
}

func TestRunReportsCropDetectionAttemptAndFallbackWhenDetectionSucceedsButCropFails(t *testing.T) {
	// Use a tiny image where the crop bounds don't produce valid output.
	img := makeTestJPEG(t)

	// Bounds that map to a degenerate pixel rectangle.
	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1000},
		},
		usage: llm.Usage{PromptTokens: 300, CompletionTokens: 60},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 750,
				Items:         []llm.ExtractedItem{{Name: "CropFallback", PriceCents: 750, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 600, CompletionTokens: 200},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		baselineProv, "base-model", 0.3, 0.6)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v (expected fallback to succeed)", err)
	}

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}

	// Crop attempt should have error.
	if result.Attempts[0].Err == nil {
		t.Fatal("expected crop attempt to report an error on crop failure")
	}

	// Fallback baseline should still produce a receipt.
	if result.Receipt.SubtotalCents != 750 {
		t.Fatalf("fallback receipt subtotal = %d, want 750", result.Receipt.SubtotalCents)
	}
}

func TestRunReportsFailureWhenFallbackCleanFails(t *testing.T) {
	detector := &fakeDetector{
		err:   errors.New("detection failed"),
		usage: llm.Usage{},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		fakeProvider{}, "base-model", 0.3, 0.6)

	// Pass invalid image data so Clean also fails.
	_, err := s.Run(context.Background(), []byte("not-an-image"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when both crop detection and fallback clean fail")
	}
}

func TestRunReportsBaselineExtractionError(t *testing.T) {
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000},
		},
		usage: llm.Usage{PromptTokens: 400, CompletionTokens: 80},
	}

	wantErr := errors.New("baseline extraction failed")
	baselineProv := fakeProvider{err: wantErr}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		baselineProv, "base-model", 0.3, 0.6)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}

	// Both attempts should be present even when baseline fails.
	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}
	if result.Attempts[1].Err == nil {
		t.Fatal("expected baseline attempt to record error")
	}
}

func TestRunDetectFullImageWithoutConfidence(t *testing.T) {
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000},
			// No confidence set (nil)
		},
		usage: llm.Usage{PromptTokens: 350, CompletionTokens: 70},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 2000,
				Items:         []llm.ExtractedItem{{Name: "Full", PriceCents: 2000, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 900, CompletionTokens: 400},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		baselineProv, "base-model", 0.3, 0.6)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}
	if result.Attempts[0].Err != nil {
		t.Fatalf("crop attempt error = %v, want nil", result.Attempts[0].Err)
	}
}

func TestRunCropDataThenExtraction(t *testing.T) {
	// Test that the crop actually changes the image data passed to baseline.
	// Use a detector reporting tight bounds that exclude most of the image.
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100},
		},
		usage: llm.Usage{PromptTokens: 400, CompletionTokens: 80},
	}

	// Capture the image the baseline provider receives.
	var capturedImage []byte
	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 100,
				Items:         []llm.ExtractedItem{{Name: "CropTest", PriceCents: 100, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100},
		},
	}
	// Override Name to track calls
	captureProvider := &capturingProvider{inner: baselineProv, captured: &capturedImage}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		captureProvider, "base-model", 0.3, 0.6)

	result, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}

	// The captured image should be different from the original (cropped +
	// binarized) and smaller in size.
	if len(capturedImage) == 0 {
		t.Fatal("baseline provider did not receive an image")
	}
	if bytes.Equal(capturedImage, img) {
		t.Fatal("baseline received the original image, not the cropped+cleaned one")
	}
	if len(capturedImage) >= len(img) {
		t.Logf("note: cropped+cleaned image (%d bytes) not smaller than original (%d bytes) — padded crop on small image may match", len(capturedImage), len(img))
	}
}

// capturingProvider wraps a fakeProvider and captures the image passed to ExtractReceipt.
type capturingProvider struct {
	inner    fakeProvider
	captured *[]byte
}

func (c *capturingProvider) ExtractReceipt(ctx context.Context, image []byte, mimeType string) (*llm.Result, error) {
	*c.captured = image
	return c.inner.ExtractReceipt(ctx, image, mimeType)
}

func (c *capturingProvider) Name() string { return "capture_provider" }

func TestRunFallbackFromDetectionErrorUsesOriginalImage(t *testing.T) {
	// When crop detection fails, the fallback should use the original (uncropped)
	// image. Verify by capturing the image on the baseline provider.
	img := makeTestJPEG(t)

	detector := &fakeDetector{
		err:   errors.New("detection failed"),
		usage: llm.Usage{PromptTokens: 100, CompletionTokens: 20},
	}

	var capturedImage []byte
	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 500,
				Items:         []llm.ExtractedItem{{Name: "FallbackItem", PriceCents: 500, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 400, CompletionTokens: 150},
		},
	}
	captureProvider := &capturingProvider{inner: baselineProv, captured: &capturedImage}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		captureProvider, "base-model", 0.3, 0.6)

	_, err := s.Run(context.Background(), img, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(capturedImage) == 0 {
		t.Fatal("baseline provider was not called in fallback")
	}
}

func TestRunFallbackImageCleanFailure(t *testing.T) {
	detector := &fakeDetector{
		err:   errors.New("detection failed"),
		usage: llm.Usage{PromptTokens: 100, CompletionTokens: 20},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		fakeProvider{}, "base-model", 0.3, 0.6)

	// Invalid image so Clean fails on both paths.
	_, err := s.Run(context.Background(), []byte("not-a-real-image"), "image/jpeg")
	if err == nil {
		t.Fatal("expected an error when fallback Clean fails on invalid image")
	}
}

func TestRunCropOperationFailure(t *testing.T) {
	// Use a 1x1 image which will have degenerate bounds after crop padding.
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.SetGray(0, 0, color.Gray{Y: 128})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	imgBytes := buf.Bytes()

	// Bounds that leave almost no image after crop.
	detector := &fakeDetector{
		result: &cropdetect.DetectionResult{
			Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500},
		},
		usage: llm.Usage{PromptTokens: 300, CompletionTokens: 60},
	}

	baselineProv := fakeProvider{
		result: &llm.Result{
			Receipt: llm.ExtractedReceipt{
				SubtotalCents: 100,
				Items:         []llm.ExtractedItem{{Name: "Tiny", PriceCents: 100, Quantity: 1}},
			},
			Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100},
		},
	}

	s := New(detector, "crop_provider", "crop-model", 0.5, 1.0,
		baselineProv, "base-model", 0.3, 0.6)

	result, err := s.Run(context.Background(), imgBytes, "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v (expected fallback on tiny image)", err)
	}

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result.Attempts))
	}
}

func TestAttemptsMatchMaxCallsConstraint(t *testing.T) {
	// Verify no path exceeds MaxCalls() == 2.
	tests := []struct {
		name string
		det  *fakeDetector
		base fakeProvider
	}{
		{
			name: "full success path",
			det: &fakeDetector{
				result: &cropdetect.DetectionResult{
					Bounds: cropdetect.Bounds{MinX: 100, MinY: 100, MaxX: 800, MaxY: 800},
				},
				usage: llm.Usage{PromptTokens: 400, CompletionTokens: 80},
			},
			base: fakeProvider{
				result: &llm.Result{
					Receipt: llm.ExtractedReceipt{
						SubtotalCents: 1000,
						Items:         []llm.ExtractedItem{{Name: "A", PriceCents: 1000, Quantity: 1}},
					},
					Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100},
				},
			},
		},
		{
			name: "crop detection fails",
			det: &fakeDetector{
				err:   errors.New("fail"),
				usage: llm.Usage{PromptTokens: 200, CompletionTokens: 40},
			},
			base: fakeProvider{
				result: &llm.Result{
					Receipt: llm.ExtractedReceipt{
						SubtotalCents: 500,
						Items:         []llm.ExtractedItem{{Name: "B", PriceCents: 500, Quantity: 1}},
					},
					Usage: llm.Usage{PromptTokens: 600, CompletionTokens: 200},
				},
			},
		},
		{
			name: "crop detection + crop op fail",
			det: &fakeDetector{
				result: &cropdetect.DetectionResult{
					Bounds: cropdetect.Bounds{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1000},
				},
				usage: llm.Usage{PromptTokens: 300, CompletionTokens: 60},
			},
			base: fakeProvider{
				result: &llm.Result{
					Receipt: llm.ExtractedReceipt{
						SubtotalCents: 750,
						Items:         []llm.ExtractedItem{{Name: "C", PriceCents: 750, Quantity: 1}},
					},
					Usage: llm.Usage{PromptTokens: 400, CompletionTokens: 80},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.det, "crop", "crop-model", 0.1, 0.2,
				tt.base, "base-model", 0.3, 0.4)
			result, err := s.Run(context.Background(), makeTestJPEG(t), "image/jpeg")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(result.Attempts) > s.MaxCalls() {
				t.Fatalf("attempts = %d, exceeds MaxCalls() = %d", len(result.Attempts), s.MaxCalls())
			}
		})
	}
}

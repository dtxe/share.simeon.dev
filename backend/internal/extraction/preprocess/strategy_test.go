package preprocess

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"share/backend/internal/llm"
)

type fakeProvider struct {
	image []byte
}

func (f *fakeProvider) ExtractReceipt(_ context.Context, image []byte, _ string) (*llm.Result, error) {
	f.image = image
	return &llm.Result{Receipt: llm.ExtractedReceipt{
		SubtotalCents: 100,
		Items:         []llm.ExtractedItem{{Name: "item", PriceCents: 100, Quantity: 1}},
	}}, nil
}

func (f *fakeProvider) Name() string { return "fake" }

func TestRunCleansImageBeforeExtraction(t *testing.T) {
	input := image.NewRGBA(image.Rect(0, 0, 2, 1))
	input.Set(0, 0, color.RGBA{R: 255, A: 255})
	input.Set(1, 0, color.RGBA{B: 255, A: 255})
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, input, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	provider := &fakeProvider{}
	strategy := New(provider, "fake-model", 1, 1)
	result, err := strategy.Run(context.Background(), encoded.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.image) == 0 || bytes.Equal(provider.image, encoded.Bytes()) {
		t.Fatal("provider did not receive a cleaned image")
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Provider != "fake" {
		t.Fatalf("attempts = %+v, want one fake attempt", result.Attempts)
	}
}

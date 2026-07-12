package ocr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"strings"
	"testing"
)

func encodeWhiteJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encoding test JPEG: %v", err)
	}
	return buf.Bytes()
}

// requireTesseract skips the test if the tesseract binary isn't on PATH —
// this package's dev-image Docker wiring installs it, but a bare host `go
// test` (outside the container) won't have it, and this test shouldn't fail
// the build in that environment.
func requireTesseract(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract not on PATH, skipping (run inside the backend dev container)")
	}
}

func TestExtractReturnsErrorOnInvalidImage(t *testing.T) {
	requireTesseract(t)

	e := New()
	_, err := e.Extract(context.Background(), []byte("not an image"))
	if err == nil {
		t.Fatal("expected an error for non-image input")
	}
}

func TestExtractOnBlankWhiteImageProducesNoError(t *testing.T) {
	requireTesseract(t)

	// A minimal valid JPEG (solid white 2x2) — asserts the subprocess
	// plumbing (temp file, invocation, stdout capture) works end to end;
	// deliberately doesn't assert on recognized text content since that's
	// tesseract-version-dependent and this image has no text at all.
	e := New()
	text, err := e.Extract(context.Background(), encodeWhiteJPEG(t))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// No assertion on non-empty text — a blank image legitimately produces
	// empty OCR output; this just confirms no error/hang on a valid image.
	_ = strings.TrimSpace(text)
}

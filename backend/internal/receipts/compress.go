package receipts

import (
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

const (
	postLLMMaxSide = 2000
	postLLMQuality = 70
)

// Compress re-encodes an existing stored receipt to JPEG quality 70, downscaling
// so the longest edge is at most 2000 pixels while preserving the aspect ratio.
// The compressed file atomically replaces the original at the same relative path.
func (s *Storage) Compress(relPath string) (int, int, error) {
	if strings.Contains(relPath, "..") {
		return 0, 0, fmt.Errorf("receipts: invalid path")
	}

	fullPath := filepath.Join(s.Dir, relPath)
	f, err := os.Open(fullPath)
	if err != nil {
		return 0, 0, fmt.Errorf("receipts: opening image for compress: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return 0, 0, fmt.Errorf("receipts: decoding image for compress: %w", err)
	}

	srcBounds := img.Bounds()
	width := srcBounds.Dx()
	height := srcBounds.Dy()

	var out image.Image
	if width > postLLMMaxSide || height > postLLMMaxSide {
		w, h := scaledDimensions(width, height, postLLMMaxSide)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, srcBounds, draw.Over, nil)
		out = dst
	} else {
		out = img
	}

	dir := filepath.Dir(fullPath)
	tmp, err := os.CreateTemp(dir, "receipt-compress-*.jpg")
	if err != nil {
		return 0, 0, fmt.Errorf("receipts: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanedUp := false
	cleanup := func() {
		if !cleanedUp {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			cleanedUp = true
		}
	}
	defer cleanup()

	if err := jpeg.Encode(tmp, out, &jpeg.Options{Quality: postLLMQuality}); err != nil {
		return 0, 0, fmt.Errorf("receipts: encoding compressed jpeg: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, 0, fmt.Errorf("receipts: closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return 0, 0, fmt.Errorf("receipts: replacing with compressed image: %w", err)
	}
	cleanedUp = true // rename consumed the temp file; don't remove it now.

	outBounds := out.Bounds()
	return outBounds.Dx(), outBounds.Dy(), nil
}

func scaledDimensions(width, height, maxSide int) (int, int) {
	if width >= height {
		h := int(math.Round(float64(height) * float64(maxSide) / float64(width)))
		if h < 1 {
			h = 1
		}
		return maxSide, h
	}
	w := int(math.Round(float64(width) * float64(maxSide) / float64(height)))
	if w < 1 {
		w = 1
	}
	return w, maxSide
}

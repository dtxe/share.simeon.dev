// Package imageprep provides deterministic receipt cleanup for experiments.
package imageprep

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
)

const jpegQuality = 80

// DefaultCropPadding is the default fraction of the crop dimensions to add
// as padding on each side (e.g. 0.05 means 5% of width/height). Applied
// after cropping to avoid cutting the receipt edge.
const DefaultCropPadding = 0.05

// CropBounds holds normalized bounds on a 0..1000 grid, where the values
// map linearly to the original image's pixel dimensions.
type CropBounds struct {
	MinX int
	MinY int
	MaxX int
	MaxY int
}

// Crop extracts a sub-rectangle from the image using normalized bounds
// (0..1000), applies a padding fraction on each side, and returns the
// resulting JPEG-encoded image. Padding is computed as a fraction of the
// crop dimensions (width and height independently), with a minimum of 3
// pixels on each side.
func Crop(imageBytes []byte, bounds CropBounds) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("imageprep: decode image: %w", err)
	}

	orig := img.Bounds()
	if orig.Empty() {
		return nil, fmt.Errorf("imageprep: image has empty bounds")
	}

	w := orig.Dx()
	h := orig.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("imageprep: invalid image dimensions: %dx%d", w, h)
	}

	// Convert normalized 0..1000 bounds to pixel coordinates.
	pixMinX := int(math.Round(float64(bounds.MinX) * float64(w) / 1000.0))
	pixMinY := int(math.Round(float64(bounds.MinY) * float64(h) / 1000.0))
	pixMaxX := int(math.Round(float64(bounds.MaxX) * float64(w) / 1000.0))
	pixMaxY := int(math.Round(float64(bounds.MaxY) * float64(h) / 1000.0))

	// Validate pixel bounds.
	if pixMinX < orig.Min.X {
		pixMinX = orig.Min.X
	}
	if pixMinY < orig.Min.Y {
		pixMinY = orig.Min.Y
	}
	if pixMaxX > orig.Max.X {
		pixMaxX = orig.Max.X
	}
	if pixMaxY > orig.Max.Y {
		pixMaxY = orig.Max.Y
	}
	if pixMinX >= pixMaxX || pixMinY >= pixMaxY {
		return nil, fmt.Errorf("imageprep: crop bounds [%d,%d,%d,%d] result in empty rectangle at pixels [%d,%d,%d,%d] (image %dx%d)",
			bounds.MinX, bounds.MinY, bounds.MaxX, bounds.MaxY, pixMinX, pixMinY, pixMaxX, pixMaxY, w, h)
	}

	// Apply padding.
	cropW := pixMaxX - pixMinX
	cropH := pixMaxY - pixMinY
	padX := int(math.Max(3, math.Round(DefaultCropPadding*float64(cropW))))
	padY := int(math.Max(3, math.Round(DefaultCropPadding*float64(cropH))))

	padMinX := pixMinX - padX
	padMinY := pixMinY - padY
	padMaxX := pixMaxX + padX
	padMaxY := pixMaxY + padY

	// Clamp to original bounds.
	if padMinX < orig.Min.X {
		padMinX = orig.Min.X
	}
	if padMinY < orig.Min.Y {
		padMinY = orig.Min.Y
	}
	if padMaxX > orig.Max.X {
		padMaxX = orig.Max.X
	}
	if padMaxY > orig.Max.Y {
		padMaxY = orig.Max.Y
	}

	cropped := image.NewRGBA(image.Rect(0, 0, padMaxX-padMinX, padMaxY-padMinY))
	for y := padMinY; y < padMaxY; y++ {
		for x := padMinX; x < padMaxX; x++ {
			cropped.Set(x-padMinX, y-padMinY, img.At(x, y))
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, cropped, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("imageprep: encode cropped jpeg: %w", err)
	}
	return out.Bytes(), nil
}

// Clean converts a receipt image to high-contrast grayscale using Otsu's
// threshold, then encodes it as JPEG for a vision extraction attempt.
func Clean(imageBytes []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("imageprep: decode image: %w", err)
	}

	bounds := img.Bounds()
	if bounds.Empty() {
		return nil, fmt.Errorf("imageprep: image has empty bounds")
	}

	gray := image.NewGray(bounds)
	histogram := [256]int{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			value := color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
			gray.SetGray(x, y, color.Gray{Y: value})
			histogram[value]++
		}
	}

	threshold := otsuThreshold(histogram, bounds.Dx()*bounds.Dy())
	for i, value := range gray.Pix {
		if value > threshold {
			gray.Pix[i] = 255
		} else {
			gray.Pix[i] = 0
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, gray, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("imageprep: encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}

func otsuThreshold(histogram [256]int, total int) uint8 {
	if total <= 0 {
		return 0
	}

	var sum int
	for value, count := range histogram {
		sum += value * count
	}

	var backgroundCount, backgroundSum int
	var bestThreshold uint8
	bestVariance := -1.0
	for threshold, count := range histogram {
		backgroundCount += count
		if backgroundCount == 0 {
			continue
		}
		foregroundCount := total - backgroundCount
		if foregroundCount == 0 {
			break
		}
		backgroundSum += threshold * count
		backgroundMean := float64(backgroundSum) / float64(backgroundCount)
		foregroundMean := float64(sum-backgroundSum) / float64(foregroundCount)
		variance := float64(backgroundCount*foregroundCount) * (backgroundMean - foregroundMean) * (backgroundMean - foregroundMean)
		if variance > bestVariance {
			bestVariance = variance
			bestThreshold = uint8(threshold)
		}
	}
	return bestThreshold
}

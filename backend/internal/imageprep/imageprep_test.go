package imageprep

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestCleanBinarizesAndPreservesDimensions(t *testing.T) {
	input := image.NewGray(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		input.SetGray(0, y, color.Gray{Y: 20})
		input.SetGray(1, y, color.Gray{Y: 40})
		input.SetGray(2, y, color.Gray{Y: 210})
		input.SetGray(3, y, color.Gray{Y: 230})
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, input, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	output, err := Clean(encoded.Bytes())
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	if got, want := decoded.Bounds(), input.Bounds(); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}

	dark := color.GrayModel.Convert(decoded.At(0, 0)).(color.Gray).Y
	light := color.GrayModel.Convert(decoded.At(3, 0)).(color.Gray).Y
	if dark > 20 || light < 235 {
		t.Fatalf("output is not high contrast: dark=%d light=%d", dark, light)
	}
}

func TestCleanRejectsInvalidImage(t *testing.T) {
	if _, err := Clean([]byte("not an image")); err == nil {
		t.Fatal("Clean succeeded for invalid image")
	}
}

func TestCropExtractsSubRegion(t *testing.T) {
	// Create a 100x100 test image with a colored rectangle in the center.
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Draw a red rectangle from (20,20) to (80,80).
	for y := 20; y < 80; y++ {
		for x := 20; x < 80; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	// Crop to the center red rectangle: pixel (20,20)-(80,80) in a 100x100
	// image maps to normalized (200,200)-(800,800) on the 0..1000 grid.
	bounds := CropBounds{MinX: 200, MinY: 200, MaxX: 800, MaxY: 800}
	output, err := Crop(encoded.Bytes(), bounds)
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	if len(output) == 0 {
		t.Fatal("Crop returned empty output")
	}

	decoded, format, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}

	// The crop should be smaller than the original (padding adds a bit,
	// but not back to 100px).
	d := decoded.Bounds()
	if d.Dx() >= 100 || d.Dy() >= 100 {
		t.Fatalf("cropped image = %dx%d, expected both < 100", d.Dx(), d.Dy())
	}
	// With bounds 200-800 on a 100px image = 20-80 pixels, plus ~5%
	// padding = about 3px on each side: 17-83 → ~66px wide.
	if d.Dx() < 50 || d.Dy() < 50 {
		t.Fatalf("cropped image = %dx%d, expected both >= 50 (padded crop region)", d.Dx(), d.Dy())
	}
}

func TestCropFullImageBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	// Full-image bounds, clamped by Crop to the original dimensions.
	bounds := CropBounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000}
	output, err := Crop(encoded.Bytes(), bounds)
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	// With padding, the crop may be the same as input (clamped to bounds).
	if decoded.Bounds().Dx() != 10 || decoded.Bounds().Dy() != 10 {
		t.Fatalf("cropped full-image bounds = %dx%d, want 10x10", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
}

func TestCropRejectsEmptyBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	// minX == maxX should produce an error.
	_, err := Crop(encoded.Bytes(), CropBounds{MinX: 500, MinY: 0, MaxX: 500, MaxY: 1000})
	if err == nil {
		t.Fatal("expected an error for empty bounds")
	}
}

func TestCropRejectsInvalidImage(t *testing.T) {
	_, err := Crop([]byte("not an image"), CropBounds{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500})
	if err == nil {
		t.Fatal("Crop succeeded for invalid image")
	}
}

func TestCropPreservesContentInCenter(t *testing.T) {
	// Create a 4x4 image with a single bright pixel at (2,2).
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetGray(x, y, color.Gray{Y: 0})
		}
	}
	img.SetGray(2, 2, color.Gray{Y: 255})

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatalf("encode input: %v", err)
	}

	// Crop tightly around the bright pixel: pixel (1,1)-(3,3) on a 4x4 image
	// is normalized bounds (250,250)-(750,750).
	bounds := CropBounds{MinX: 250, MinY: 250, MaxX: 750, MaxY: 750}
	output, err := Crop(encoded.Bytes(), bounds)
	if err != nil {
		t.Fatalf("Crop: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}

	// The small crop (~2px from padding on each side of 2px original
	// region) should still contain bright pixels (JPEG compression
	// may soften them but the brightness should remain).
	hasBright := false
	for y := decoded.Bounds().Min.Y; y < decoded.Bounds().Max.Y; y++ {
		for x := decoded.Bounds().Min.X; x < decoded.Bounds().Max.X; x++ {
			r, g, b, _ := decoded.At(x, y).RGBA()
			if r > 200<<8 && g > 200<<8 && b > 200<<8 {
				hasBright = true
			}
		}
	}
	if !hasBright {
		t.Fatal("cropped image does not contain the bright pixel")
	}
}

package receipts

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestCompressDownscalesLargeImage(t *testing.T) {
	storage := New(t.TempDir())
	relPath := "session-a/receipt.jpg"
	writeTestJPEG(t, filepath.Join(storage.Dir, relPath), 3000, 2500)

	w, h, err := storage.Compress(relPath)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	if w != 2000 || h != 1667 {
		t.Fatalf("expected 2000x1667, got %dx%d", w, h)
	}

	// Verify the on-disk file is the compressed one and there are no temp leftovers.
	verifyDecoded(t, storage, relPath, 2000, 1667)
	verifyNoTempFiles(t, filepath.Join(storage.Dir, "session-a"))
}

func TestCompressReEncodesSmallImageWithoutUpscaling(t *testing.T) {
	storage := New(t.TempDir())
	relPath := "session-b/receipt.jpg"
	writeTestJPEG(t, filepath.Join(storage.Dir, relPath), 800, 600)

	w, h, err := storage.Compress(relPath)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	if w != 800 || h != 600 {
		t.Fatalf("expected dimensions unchanged at 800x600, got %dx%d", w, h)
	}

	verifyDecoded(t, storage, relPath, 800, 600)
}

func TestCompressMissingFile(t *testing.T) {
	storage := New(t.TempDir())
	_, _, err := storage.Compress("session-c/missing.jpg")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func writeTestJPEG(t testing.TB, path string, width, height int) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating test image: %v", err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encoding test jpeg: %v", err)
	}
}

func verifyDecoded(t testing.TB, storage *Storage, relPath string, wantW, wantH int) {
	t.Helper()

	f, err := storage.Open(relPath)
	if err != nil {
		t.Fatalf("opening compressed file: %v", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decoding compressed file: %v", err)
	}
	if gotW, gotH := img.Bounds().Dx(), img.Bounds().Dy(); gotW != wantW || gotH != wantH {
		t.Fatalf("compressed on-disk dimensions %dx%d, want %dx%d", gotW, gotH, wantW, wantH)
	}
}

func verifyNoTempFiles(t testing.TB, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading storage dir: %v", err)
	}
	for _, e := range entries {
		if matched, _ := filepath.Match("receipt-compress-*.jpg", e.Name()); matched {
			t.Fatalf("leftover temp file in storage dir: %s", e.Name())
		}
	}
}

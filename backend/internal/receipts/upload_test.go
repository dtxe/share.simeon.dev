package receipts

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

func TestSaveRejectsUploadOverByteLimit(t *testing.T) {
	storage := New(t.TempDir())
	_, err := storage.Save("session-id", bytes.NewReader(make([]byte, MaxUploadBytes+1)))
	if err == nil {
		t.Fatal("expected oversized upload to be rejected")
	}
	if !strings.Contains(err.Error(), "upload exceeds") {
		t.Fatalf("expected byte-limit error, got %v", err)
	}
}

func TestSaveRejectsImageOverPixelLimit(t *testing.T) {
	storage := New(t.TempDir())
	img := pngWithDimensions(MaxImagePixels+1, 1)

	_, err := storage.Save("session-id", bytes.NewReader(img))
	if err == nil {
		t.Fatal("expected oversized image dimensions to be rejected")
	}
	if !strings.Contains(err.Error(), "image dimensions too large") {
		t.Fatalf("expected dimension-limit error, got %v", err)
	}
}

func TestSaveRejectsImageOverSideLimit(t *testing.T) {
	storage := New(t.TempDir())
	img := pngWithDimensions(MaxImageSide+1, 1)

	_, err := storage.Save("session-id", bytes.NewReader(img))
	if err == nil {
		t.Fatal("expected oversized image dimensions to be rejected")
	}
	if !strings.Contains(err.Error(), "image dimensions too large") {
		t.Fatalf("expected dimension-limit error, got %v", err)
	}
}

func pngWithDimensions(width, height int) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writePNGChunk(&out, "IHDR", ihdr(width, height))
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes()
}

func ihdr(width, height int) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], uint32(width))
	binary.BigEndian.PutUint32(data[4:8], uint32(height))
	data[8] = 8 // bit depth
	data[9] = 2 // truecolor
	return data
}

func writePNGChunk(out *bytes.Buffer, chunkType string, data []byte) {
	binary.Write(out, binary.BigEndian, uint32(len(data)))
	out.WriteString(chunkType)
	out.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	binary.Write(out, binary.BigEndian, crc.Sum32())
}

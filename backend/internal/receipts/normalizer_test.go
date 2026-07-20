package receipts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
)

type testNormalizer struct{}

func (testNormalizer) Normalize(_ context.Context, r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

type recordingNormalizer struct{ called bool }

func (n *recordingNormalizer) Normalize(_ context.Context, r io.Reader) ([]byte, error) {
	n.called = true
	return io.ReadAll(r)
}

func TestLocalStorageUsesInjectedNormalizer(t *testing.T) {
	n := &recordingNormalizer{}
	s := New(t.TempDir(), n)
	if _, err := s.Save(context.Background(), "session", bytes.NewReader([]byte("normalized"))); err != nil {
		t.Fatal(err)
	}
	if !n.called {
		t.Fatal("normalizer was not called")
	}
}

type rejectingNormalizer struct{}

func (rejectingNormalizer) Normalize(_ context.Context, r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxUploadBytes {
		return nil, fmt.Errorf("upload exceeds %d", MaxUploadBytes)
	}
	if len(b) >= 24 && bytes.Equal(b[:8], []byte{137, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		w := int(binary.BigEndian.Uint32(b[16:20]))
		h := int(binary.BigEndian.Uint32(b[20:24]))
		if w > MaxImageSide || h > MaxImageSide || int64(w)*int64(h) > MaxImagePixels {
			return nil, fmt.Errorf("image dimensions too large")
		}
	}
	return b, nil
}

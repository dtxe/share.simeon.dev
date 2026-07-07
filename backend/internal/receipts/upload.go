// Package receipts handles receipt image uploads: validating what was
// actually sent (not what the client claims it sent), guarding against
// decompression bombs, and re-encoding everything to a normalized JPEG
// before it ever touches disk or gets served back out.
package receipts

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decoder registration
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp" // decoder registration (decode-only, no encoder needed)
)

const (
	MaxUploadBytes = 10 << 20 // 10 MiB
	MaxImagePixels = 4096 * 2160
	MaxImageSide   = 4096
	jpegQuality    = 80
)

var allowedMimeTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

type Storage struct {
	Dir string
}

func New(dir string) *Storage {
	return &Storage{Dir: dir}
}

// Save validates and stores an uploaded receipt image, returning the path
// (relative to Dir) to persist on the bill_sessions row. The returned path
// component is always server-generated — no byte of the client's original
// filename is used, which is what makes path traversal / overwrite
// collisions a non-issue here.
func (s *Storage) Save(sessionID string, r io.Reader) (relPath string, err error) {
	if !isSafePathComponent(sessionID) {
		return "", fmt.Errorf("receipts: invalid session id")
	}

	limited := io.LimitReader(r, MaxUploadBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("receipts: reading upload: %w", err)
	}
	if len(raw) > MaxUploadBytes {
		return "", fmt.Errorf("receipts: upload exceeds %d bytes", MaxUploadBytes)
	}

	sniffLen := 512
	if len(raw) < sniffLen {
		sniffLen = len(raw)
	}
	detected := http.DetectContentType(raw[:sniffLen])
	if !allowedMimeTypes[detected] {
		return "", fmt.Errorf("receipts: unsupported content type %q", detected)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("receipts: reading image dimensions: %w", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return "", fmt.Errorf("receipts: image dimensions too large (%dx%d)", cfg.Width, cfg.Height)
	}
	if cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return "", fmt.Errorf("receipts: image dimensions too large (%dx%d)", cfg.Width, cfg.Height)
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("receipts: decoding image: %w", err)
	}

	dir := filepath.Join(s.Dir, sessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("receipts: creating storage dir: %w", err)
	}

	name, err := randomFilename()
	if err != nil {
		return "", err
	}
	relPath = filepath.Join(sessionID, name)
	fullPath := filepath.Join(s.Dir, relPath)

	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("receipts: creating file: %w", err)
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return "", fmt.Errorf("receipts: re-encoding as jpeg: %w", err)
	}

	return relPath, nil
}

// Open returns the stored, re-encoded JPEG bytes for serving.
func (s *Storage) Open(relPath string) (*os.File, error) {
	if strings.Contains(relPath, "..") {
		return nil, fmt.Errorf("receipts: invalid path")
	}
	return os.Open(filepath.Join(s.Dir, relPath))
}

// Delete removes a stored receipt (used by the cleanup sweep once its bill
// has expired) and best-effort removes the now-possibly-empty per-session
// directory it lived in.
func (s *Storage) Delete(relPath string) error {
	if strings.Contains(relPath, "..") {
		return fmt.Errorf("receipts: invalid path")
	}
	full := filepath.Join(s.Dir, relPath)
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(filepath.Dir(full)) // no-op if not empty
	return nil
}

// isSafePathComponent rejects anything that could escape s.Dir when joined
// into a path — belt-and-suspenders on top of the caller already having
// resolved sessionID against an owner-scoped DB row.
func isSafePathComponent(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\")
}

func randomFilename() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ".jpg", nil
}

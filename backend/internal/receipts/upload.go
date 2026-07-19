// Package receipts handles receipt image uploads: validating what was
// actually sent (not what the client claims it sent), guarding against
// decompression bombs, and re-encoding everything to a normalized JPEG
// before it ever touches disk or gets served back out.
package receipts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	_ "image/png" // decoder registration
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

type ReceiptStorage interface {
	Save(context.Context, string, io.Reader) (string, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	Compress(context.Context, string) (int, int, error)
	PresignGet(context.Context, string, time.Duration) (string, error)
	PresignHead(context.Context, string, time.Duration) (string, error)
}

type LocalStorage struct {
	Dir string
}
type Storage = LocalStorage // retained for local callers; ReceiptStorage is the runtime abstraction.

func New(dir string) *LocalStorage {
	return &LocalStorage{Dir: dir}
}

// Save validates and stores an uploaded receipt image, returning the path
// (relative to Dir) to persist on the bill_sessions row. The returned path
// component is always server-generated — no byte of the client's original
// filename is used, which is what makes path traversal / overwrite
// collisions a non-issue here.
func (s *LocalStorage) Save(ctx context.Context, sessionID string, r io.Reader) (relPath string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !isSafePathComponent(sessionID) {
		return "", fmt.Errorf("receipts: invalid session id")
	}

	data, err := normalizedJPEG(r)
	if err != nil {
		return "", err
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

	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("receipts: writing normalized jpeg: %w", err)
	}

	return relPath, nil
}

// Open returns the stored, re-encoded JPEG bytes for serving.
func (s *LocalStorage) Open(ctx context.Context, relPath string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clean, err := NormalizePath(relPath)
	if err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(s.Dir, filepath.FromSlash(clean)))
}

// Delete removes a stored receipt (used by the cleanup sweep once its bill
// has expired) and best-effort removes the now-possibly-empty per-session
// directory it lived in.
func (s *LocalStorage) Delete(ctx context.Context, relPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := NormalizePath(relPath)
	if err != nil {
		return err
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(clean))
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(filepath.Dir(full)) // no-op if not empty
	return nil
}

func (s *LocalStorage) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", fmt.Errorf("receipts: presigning unsupported by local storage")
}
func (s *LocalStorage) PresignHead(context.Context, string, time.Duration) (string, error) {
	return "", fmt.Errorf("receipts: presigning unsupported by local storage")
}

// NormalizePath validates the persisted path and returns its slash-normalized form.
func NormalizePath(p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || filepath.IsAbs(p) {
		return "", fmt.Errorf("receipts: invalid path")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("receipts: invalid path")
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 2 || !isSafePathComponent(parts[0]) || !strings.HasSuffix(parts[1], ".jpg") || !isSafePathComponent(parts[1][:len(parts[1])-4]) {
		return "", fmt.Errorf("receipts: invalid path")
	}
	return clean, nil
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

// Package ocr shells out to the tesseract CLI to transcribe raw text from a
// receipt photo, for extraction strategy 03
// (docs/plans/03-strategy-ocr-first.md). No well-maintained pure-Go OCR
// engine exists, and cgo bindings (gosseract) don't remove the
// system-dependency problem while adding a build-complexity tax on top — a
// subprocess call is simpler. Dev-image only for now: the tesseract binary
// is installed in docker/backend.Dockerfile's dev stage, not the distroless
// prod stage — see the plan doc for why that's deliberately deferred.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const defaultTimeout = 30 * time.Second

// Engine runs tesseract as a subprocess against a temp file. The zero value
// is usable (falls back to defaultTimeout).
type Engine struct {
	// Timeout bounds the tesseract subprocess call. Defaults to 30s.
	Timeout time.Duration
	// PSM is tesseract's --psm (page segmentation mode) flag. Defaults to
	// "6" (assume a single uniform block of text), a reasonable starting
	// point for a receipt photo that's already roughly cropped to the
	// receipt itself; tune against real corpus receipts if accuracy is
	// poor.
	PSM string
}

func New() *Engine {
	return &Engine{Timeout: defaultTimeout, PSM: "6"}
}

// Extract writes image to a temp file and runs `tesseract <path> stdout`,
// returning the raw recognized text. Context deadline/cancellation and the
// Engine's own Timeout both bound the subprocess.
func (e *Engine) Extract(ctx context.Context, image []byte) (string, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	psm := e.PSM
	if psm == "" {
		psm = "6"
	}

	tmp, err := os.CreateTemp("", "ocr-*.jpg")
	if err != nil {
		return "", fmt.Errorf("ocr: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(image); err != nil {
		tmp.Close()
		return "", fmt.Errorf("ocr: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("ocr: closing temp file: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "tesseract", tmpPath, "stdout", "--psm", psm)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ocr: tesseract: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

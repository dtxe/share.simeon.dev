package imageconverter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	MaxSourceBytes        = 10 << 20
	MaxOutputBytes        = 20 << 20
	MaxInputSide          = 8192
	MaxInputPixels  int64 = 40_000_000
	MaxOutputSide         = 4096
	MaxOutputPixels int64 = 4096 * 2160
	MaxStderrBytes        = 64 << 10
)

var (
	ErrInvalid        = errors.New("invalid image")
	ErrUnavailable    = errors.New("image converter unavailable")
	ErrBusy           = errors.New("image converter busy")
	ErrOutputLimit    = errors.New("image converter output limit exceeded")
	ErrOutputOverflow = errors.New("image converter output overflow")
)

type Kind int

const (
	Invalid Kind = iota
	JPEG
	PNG
	WEBP
	HEIC
	HEIF
	AVIF
)

type Category int

const (
	CategoryInvalid Category = iota
	CategoryUnavailable
)

type Error struct {
	Category Category
	Err      error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
func invalid(err error) error {
	return &Error{Category: CategoryInvalid, Err: fmt.Errorf("%w: %v", ErrInvalid, err)}
}
func unavailable(err error) error {
	return &Error{Category: CategoryUnavailable, Err: fmt.Errorf("%w: %v", ErrUnavailable, err)}
}

type RunResult struct {
	Stdout         []byte
	Stderr         []byte
	StdoutOverflow bool
	StderrOverflow bool
}

// Runner keeps command execution replaceable in tests and ensures callers can
// distinguish a capped stream from a genuine command result.
type Runner interface {
	Run(context.Context, []string, []byte) (RunResult, error)
}

type ExecRunner struct{ Exe string }

func (r ExecRunner) Run(ctx context.Context, args []string, input []byte) (RunResult, error) {
	cmd := exec.CommandContext(ctx, r.Exe, args...)
	cmd.Stdin = bytes.NewReader(input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, err
	}
	out := &cappedBuffer{max: MaxOutputBytes}
	errOut := &cappedBuffer{max: MaxStderrBytes}
	if err := cmd.Start(); err != nil {
		return RunResult{}, err
	}
	stdoutDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(out, stdout)
		if errors.Is(copyErr, ErrOutputOverflow) {
			_ = cmd.Process.Kill()
		}
		stdoutDone <- copyErr
	}()
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(errOut, stderr)
		if errors.Is(copyErr, ErrOutputOverflow) {
			_ = cmd.Process.Kill()
		}
		stderrDone <- copyErr
	}()
	waitErr := cmd.Wait()
	stdoutErr, stderrErr := <-stdoutDone, <-stderrDone
	result := RunResult{Stdout: out.Bytes(), Stderr: errOut.Bytes(), StdoutOverflow: out.Overflow(), StderrOverflow: errOut.Overflow()}
	if result.StdoutOverflow || result.StderrOverflow {
		return result, ErrOutputOverflow
	}
	if stdoutErr != nil {
		return result, stdoutErr
	}
	if stderrErr != nil {
		return result, stderrErr
	}
	return result, waitErr
}

type cappedBuffer struct {
	b        bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p)+b.b.Len() > b.max {
		n := b.max - b.b.Len()
		if n > 0 {
			_, _ = b.b.Write(p[:n])
		}
		b.overflow = true
		return 0, ErrOutputOverflow
	}
	return b.b.Write(p)
}
func (b *cappedBuffer) Bytes() []byte  { return b.b.Bytes() }
func (b *cappedBuffer) Overflow() bool { return b.overflow }

type Service struct {
	Runner        Runner
	sem           chan struct{}
	MaxConcurrent int
	ready         atomic.Bool
}

func New(r Runner, maxConcurrent int) *Service {
	if r == nil {
		panic("imageconverter: nil runner")
	}
	if maxConcurrent < 1 {
		maxConcurrent = 4
	}
	return &Service{Runner: r, sem: make(chan struct{}, maxConcurrent), MaxConcurrent: maxConcurrent}
}

func (s *Service) Preflight(ctx context.Context) error {
	result, err := s.Runner.Run(ctx, []string{"-version"}, nil)
	if err != nil || result.StdoutOverflow || result.StderrOverflow {
		s.ready.Store(false)
		if err == nil {
			err = errors.New("preflight output limit")
		}
		return unavailable(err)
	}
	s.ready.Store(true)
	return nil
}

func (s *Service) Ready() bool         { return s.ready.Load() }
func (s *Service) SetReady(ready bool) { s.ready.Store(ready) }

type Result struct {
	JPEG          []byte
	Width, Height int
}

func (s *Service) Convert(ctx context.Context, source []byte) (Result, error) {
	if len(source) == 0 || len(source) > MaxSourceBytes {
		return Result{}, invalid(errors.New("source size"))
	}
	kind := Detect(source)
	if kind == Invalid {
		return Result{}, invalid(errors.New("source format"))
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return Result{}, unavailable(ErrBusy)
	}
	_, err := s.identify(ctx, source, kind)
	if err != nil {
		return Result{}, err
	}
	args := []string{
		"convert",
		"-limit", "thread", "2", "-limit", "time", "20",
		"-limit", "memory", "256MiB", "-limit", "map", "512MiB",
		"-limit", "disk", "1GiB", "-limit", "width", "8192",
		"-limit", "height", "8192", "-limit", "area", "40MP",
		"-limit", "list-length", "1", "-", "-auto-orient",
		"-resize", "4096x4096>", "-resize", "8847360@>",
		"-background", "white", "-alpha", "remove",
		"-colorspace", "sRGB", "-strip", "-quality", "95", "jpeg:-",
	}
	run, runErr := s.Runner.Run(ctx, args, source)
	if run.StdoutOverflow || run.StderrOverflow || errors.Is(runErr, ErrOutputOverflow) || len(run.Stdout) > MaxOutputBytes {
		return Result{}, unavailable(ErrOutputLimit)
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{}, unavailable(ctx.Err())
		}
		return Result{}, unavailable(runErr)
	}
	w, h, ok := jpegDimensions(run.Stdout)
	if !ok || w <= 0 || h <= 0 || w > MaxOutputSide || h > MaxOutputSide || int64(w)*int64(h) > MaxOutputPixels {
		return Result{}, unavailable(errors.New("invalid converter output"))
	}
	return Result{JPEG: run.Stdout, Width: w, Height: h}, nil
}

type metadata struct{ width, height int }

func (s *Service) identify(ctx context.Context, source []byte, expected Kind) (metadata, error) {
	run, err := s.Runner.Run(ctx, []string{"identify", "-limit", "thread", "2", "-limit", "time", "20", "-limit", "memory", "256MiB", "-limit", "map", "512MiB", "-limit", "disk", "1GiB", "-limit", "width", "8192", "-limit", "height", "8192", "-limit", "area", "40MP", "-limit", "list-length", "1", "-ping", "-format", "%m|%w|%h|%n|%[scene]\\n", "-"}, source)
	if run.StdoutOverflow || run.StderrOverflow || errors.Is(err, ErrOutputOverflow) {
		if ctx.Err() != nil {
			return metadata{}, unavailable(ctx.Err())
		}
		return metadata{}, unavailable(ErrOutputLimit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return metadata{}, unavailable(ctx.Err())
		}
		return metadata{}, invalid(errors.New("identify failed"))
	}
	text := strings.TrimSpace(string(run.Stdout))
	lines := strings.Split(text, "\n")
	if len(lines) != 1 || text == "" {
		return metadata{}, invalid(errors.New("multiple frames"))
	}
	parts := strings.Split(lines[0], "|")
	if len(parts) != 5 {
		return metadata{}, invalid(errors.New("identify output"))
	}
	gotKind, ok := kindFromName(parts[0])
	if !ok || !kindsAgree(expected, gotKind) {
		return metadata{}, invalid(errors.New("format mismatch"))
	}
	w, e1 := strconv.Atoi(parts[1])
	h, e2 := strconv.Atoi(parts[2])
	frames, e3 := strconv.Atoi(parts[3])
	scene, e4 := strconv.Atoi(parts[4])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || w <= 0 || h <= 0 || frames != 1 || scene != 0 {
		return metadata{}, invalid(errors.New("invalid geometry"))
	}
	if w > MaxInputSide || h > MaxInputSide || int64(w)*int64(h) > MaxInputPixels {
		return metadata{}, invalid(errors.New("dimensions too large"))
	}
	return metadata{width: w, height: h}, nil
}

func kindsAgree(expected, actual Kind) bool {
	if expected == actual {
		return true
	}
	return isHEIFFamily(expected) && isHEIFFamily(actual)
}
func isHEIFFamily(k Kind) bool { return k == HEIC || k == HEIF || k == AVIF }

func kindFromName(name string) (Kind, bool) {
	switch name {
	case "JPEG":
		return JPEG, true
	case "PNG":
		return PNG, true
	case "WEBP":
		return WEBP, true
	case "HEIC":
		return HEIC, true
	case "HEIF":
		return HEIF, true
	case "AVIF":
		return AVIF, true
	}
	return Invalid, false
}

func Detect(b []byte) Kind {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return JPEG
	}
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return PNG
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return WEBP
	}
	if k, ok := bmffKind(b); ok {
		return k
	}
	return Invalid
}

func bmffKind(b []byte) (Kind, bool) {
	if len(b) < 16 || string(b[4:8]) != "ftyp" {
		return Invalid, false
	}
	size32 := binary.BigEndian.Uint32(b[:4])
	headerSize := 8
	var size uint64 = uint64(size32)
	if size32 == 1 {
		if len(b) < 24 {
			return Invalid, false
		}
		size = binary.BigEndian.Uint64(b[8:16])
		headerSize = 16
	} else if size32 == 0 {
		size = uint64(len(b))
	}
	if size < uint64(headerSize+8) || size > uint64(len(b)) {
		return Invalid, false
	}
	brands := []string{string(b[headerSize : headerSize+4])}
	for i := headerSize + 8; i+4 <= int(size); i += 4 {
		brands = append(brands, string(b[i:i+4]))
	}
	for _, brand := range brands {
		switch brand {
		case "avif", "avis":
			return AVIF, true
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs":
			return HEIC, true
		case "mif1", "msf1":
			return HEIF, true
		}
	}
	return Invalid, false
}

func jpegDimensions(b []byte) (int, int, bool) {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 0, 0, false
	}
	for i := 2; i+9 < len(b); {
		if b[i] != 0xff {
			i++
			continue
		}
		for i < len(b) && b[i] == 0xff {
			i++
		}
		if i >= len(b) {
			break
		}
		marker := b[i]
		i++
		if marker == 0xd9 || marker == 0xda {
			break
		}
		if marker >= 0xd0 && marker <= 0xd7 || marker == 0x01 {
			continue
		}
		if i+2 > len(b) {
			break
		}
		n := int(binary.BigEndian.Uint16(b[i : i+2]))
		if n < 2 || i+n > len(b) {
			break
		}
		if (marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) || (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf) {
			if n < 7 {
				return 0, 0, false
			}
			return int(binary.BigEndian.Uint16(b[i+5 : i+7])), int(binary.BigEndian.Uint16(b[i+3 : i+5])), true
		}
		i += n
	}
	return 0, 0, false
}

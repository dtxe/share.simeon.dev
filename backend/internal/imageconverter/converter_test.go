package imageconverter

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	identify string
	output   []byte
	args     [][]string
	err      error
	overflow bool
}

func (f *fakeRunner) Run(_ context.Context, args []string, _ []byte) (RunResult, error) {
	f.args = append(f.args, append([]string(nil), args...))
	if args[0] == "identify" {
		return RunResult{Stdout: []byte(f.identify)}, f.err
	}
	return RunResult{Stdout: f.output, StdoutOverflow: f.overflow}, f.err
}
func fakeJPEG(w, h int) []byte {
	return []byte{0xff, 0xd8, 0xff, 0xc0, 0, 11, 8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0, 0xff, 0xd9}
}
func pngMagic() []byte { return []byte{137, 80, 78, 71, 13, 10, 26, 10} }
func bmff(brand string, size int) []byte {
	b := make([]byte, size)
	b[0] = byte(size >> 24)
	b[1] = byte(size >> 16)
	b[2] = byte(size >> 8)
	b[3] = byte(size)
	copy(b[4:], "ftyp")
	copy(b[8:], brand)
	return b
}
func extendedBMFF(brand string, size uint64) []byte {
	b := make([]byte, 24)
	b[3] = 1
	copy(b[4:], "ftyp")
	for i := 0; i < 8; i++ {
		b[15-i] = byte(size >> (8 * i))
	}
	copy(b[16:], brand)
	return b
}

func TestDetectBrandsAndMalformedBMFF(t *testing.T) {
	if Detect(pngMagic()) != PNG {
		t.Fatal("PNG not detected")
	}
	for _, tc := range []struct {
		brand string
		want  Kind
	}{{"heic", HEIC}, {"heix", HEIC}, {"heim", HEIC}, {"heis", HEIC}, {"hevm", HEIC}, {"hevs", HEIC}, {"mif1", HEIF}, {"avif", AVIF}, {"avis", AVIF}} {
		if got := Detect(bmff(tc.brand, 20)); got != tc.want {
			t.Errorf("%s: got %v", tc.brand, got)
		}
	}
	if Detect(append(bmff("avif", 20), []byte("xxxx")...)) != AVIF {
		t.Fatal("valid ftyp rejected")
	}
	bad := bmff("avif", 20)
	bad[3] = 40
	if Detect(bad) != Invalid {
		t.Fatal("oversized ftyp accepted")
	}
	if Detect([]byte{0, 0, 0, 16, 'f', 't', 'y', 'p'}) != Invalid {
		t.Fatal("short BMFF accepted")
	}
	if Detect(extendedBMFF("heis", 24)) != HEIC {
		t.Fatal("extended-size ftyp rejected")
	}
	if Detect(append(extendedBMFF("avif", 40), make([]byte, 8)...)) != Invalid {
		t.Fatal("extended-size box with unavailable bytes accepted")
	}
}

func TestServiceIdentifyAndConversionArgs(t *testing.T) {
	f := &fakeRunner{identify: "JPEG|3000|2000|1|0\n", output: fakeJPEG(3000, 2000)}
	s := New(f, 2)
	got, err := s.Convert(context.Background(), []byte{0xff, 0xd8, 0xff})
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 3000 || got.Height != 2000 {
		t.Fatalf("got %dx%d", got.Width, got.Height)
	}
	if len(f.args) != 2 || f.args[1][0] != "convert" {
		t.Fatalf("unexpected args %#v", f.args)
	}
	want := []string{"convert", "-limit", "thread", "2", "-limit", "time", "20", "-limit", "memory", "256MiB", "-limit", "map", "512MiB", "-limit", "disk", "1GiB", "-limit", "width", "8192", "-limit", "height", "8192", "-limit", "area", "40MP", "-limit", "list-length", "1", "-", "-auto-orient", "-resize", "4096x4096>", "-resize", "8847360@>", "-background", "white", "-alpha", "remove", "-colorspace", "sRGB", "-strip", "-quality", "95", "jpeg:-"}
	if strings.Join(f.args[1], " ") != strings.Join(want, " ") {
		t.Fatalf("args %v", f.args[1])
	}
}

func TestHEIFIdentifyAliasesAreAllowedOnlyWithinFamily(t *testing.T) {
	for _, name := range []string{"HEIC", "HEIF", "AVIF"} {
		f := &fakeRunner{identify: name + "|1|1|1|0\n", output: fakeJPEG(1, 1)}
		if _, err := New(f, 1).Convert(context.Background(), bmff("heic", 20)); err != nil {
			t.Errorf("alias %s rejected: %v", name, err)
		}
	}
	f := &fakeRunner{identify: "PNG|1|1|1|0\n", output: fakeJPEG(1, 1)}
	if _, err := New(f, 1).Convert(context.Background(), []byte{0xff, 0xd8, 0xff}); err == nil {
		t.Fatal("JPEG/PNG mismatch accepted")
	}
}

func TestServiceRejectsIdentifyProblemsAndOutputOverflow(t *testing.T) {
	for _, id := range []string{"JPEG|1|1|2|0\n", "PNG|1|1|1|0\n", "JPEG|9000|1|1|0\n"} {
		f := &fakeRunner{identify: id}
		_, err := New(f, 1).Convert(context.Background(), []byte{0xff, 0xd8, 0xff})
		var ce *Error
		if !errors.As(err, &ce) || ce.Category != CategoryInvalid {
			t.Errorf("%q: %v", id, err)
		}
	}
	f := &fakeRunner{identify: "JPEG|1|1|1|0\n", output: fakeJPEG(1, 1), overflow: true}
	_, err := New(f, 1).Convert(context.Background(), []byte{0xff, 0xd8, 0xff})
	var ce *Error
	if !errors.As(err, &ce) || ce.Category != CategoryUnavailable {
		t.Fatalf("overflow: %v", err)
	}
}

func TestHTTPStatusesAndHealth(t *testing.T) {
	f := &fakeRunner{identify: "JPEG|1|1|1|0\n", output: fakeJPEG(1, 1)}
	s := New(f, 1)
	h := s.Handler()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready health %d", w.Code)
	}
	s.SetReady(true)
	r = httptest.NewRequest(http.MethodPost, "/healthz", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("health method %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/convert", bytes.NewReader([]byte("x")))
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("content type %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/convert", bytes.NewReader([]byte{0xff, 0xd8, 0xff}))
	r.Header.Set("Content-Type", "application/octet-stream")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("convert %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestClientValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(fakeJPEG(1, 1))
	}))
	defer srv.Close()
	out, err := NewClient(srv.URL, 0).Normalize(context.Background(), bytes.NewReader([]byte("raw")))
	if err != nil || len(out) == 0 {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusBadGateway) }))
	defer bad.Close()
	if _, err := NewClient(bad.URL, 0).Normalize(context.Background(), bytes.NewReader(nil)); err == nil {
		t.Fatal("expected status failure")
	}
	invalidOutput := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("not jpeg"))
	}))
	defer invalidOutput.Close()
	if _, err := NewClient(invalidOutput.URL, 0).Normalize(context.Background(), bytes.NewReader(nil)); err == nil {
		t.Fatal("expected output validation failure")
	}
}

func TestExecRunnerCancellation(t *testing.T) {
	if err := os.Setenv("GO_WANT_IMAGE_CONVERTER_HELPER", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("GO_WANT_IMAGE_CONVERTER_HELPER") })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runner := ExecRunner{Exe: os.Args[0]}
	start := time.Now()
	_, err := runner.Run(ctx, []string{"-test.run=TestImageConverterHelperProcess", "--"}, nil)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation did not stop helper promptly: err=%v", err)
	}
}

func TestImageConverterHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_IMAGE_CONVERTER_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}

package imageconverter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MultipartOverhead = 64 << 10

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("ok"))
		}
	})
	mux.HandleFunc("/convert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/octet-stream" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxSourceBytes+1))
		if err != nil || len(body) > MaxSourceBytes {
			http.Error(w, "invalid image", http.StatusBadRequest)
			return
		}
		result, err := s.Convert(r.Context(), body)
		if err != nil {
			var ce *Error
			if errors.As(err, &ce) && ce.Category == CategoryInvalid {
				http.Error(w, "invalid image", http.StatusBadRequest)
			} else {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Image-Width", strconv.Itoa(result.Width))
		w.Header().Set("X-Image-Height", strconv.Itoa(result.Height))
		w.Header().Set("Content-Length", strconv.Itoa(len(result.JPEG)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.JPEG)
	})
	return mux
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(base string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) Normalize(ctx context.Context, r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxSourceBytes {
		return nil, &Error{Category: CategoryInvalid, Err: ErrInvalid}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/convert", bytes.NewReader(b))
	if err != nil {
		return nil, unavailable(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, unavailable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, unavailable(errors.New("converter request failed"))
		}
		return nil, &Error{Category: CategoryInvalid, Err: ErrInvalid}
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, MaxOutputBytes+1))
	if err != nil || len(out) > MaxOutputBytes {
		return nil, unavailable(ErrOutputLimit)
	}
	if strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]) != "image/jpeg" {
		return nil, unavailable(errors.New("invalid converter content type"))
	}
	w, h, ok := jpegDimensions(out)
	if !ok || w <= 0 || h <= 0 || w > MaxOutputSide || h > MaxOutputSide || int64(w)*int64(h) > MaxOutputPixels {
		return nil, unavailable(errors.New("invalid converter image"))
	}
	return out, nil
}

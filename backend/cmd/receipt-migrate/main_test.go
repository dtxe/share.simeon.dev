package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"share/backend/internal/config"
	"share/backend/internal/receipts"
)

type preflightStore struct {
	data      []byte
	getErr    error
	deleteErr error
	deleted   int
	puts      int
}

func (s *preflightStore) Head(context.Context, string) (receipts.ObjectInfo, error) {
	return receipts.ObjectInfo{}, nil
}

func (s *preflightStore) Put(context.Context, string, io.Reader, int64, string) error {
	s.puts++
	return nil
}

func (s *preflightStore) Get(context.Context, string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return io.NopCloser(bytes.NewReader([]byte("share receipt migration canary"))), nil
}

func (s *preflightStore) Delete(context.Context, string) error {
	s.deleted++
	return s.deleteErr
}

type fakeOps struct{ version bool }

func (fakeOps) HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
}

func (f fakeOps) GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	return &s3.GetBucketVersioningOutput{}, nil
}

type fakeSigner struct {
	url  string
	fail bool
}

func (f fakeSigner) PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	if f.fail {
		return nil, io.ErrUnexpectedEOF
	}
	return &v4.PresignedHTTPRequest{URL: f.url}, nil
}

type rewriteTransport struct {
	target *url.URL
	client *http.Client
}

func (rt *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	u.Scheme = rt.target.Scheme
	u.Host = rt.target.Host
	q, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), r.Body)
	if err != nil {
		return nil, err
	}
	q.Header = r.Header
	return rt.client.Do(q)
}

func signedClient(server *httptest.Server) *http.Client {
	base := server.Client()
	base.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	target, _ := url.Parse(server.URL)
	return &http.Client{
		Transport: &rewriteTransport{target: target, client: base},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

const fakeHost = "presigned.test"

func signedURL(path string) string {
	return "https://" + fakeHost + path
}

func testConfig() *config.MigrationConfig {
	return &config.MigrationConfig{S3Bucket: "bucket", S3Prefix: "receipts", S3ProxyHost: fakeHost}
}

func TestPreflightPresignedBytesPrivateAndCleanup(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery != "":
			w.Write([]byte("share receipt migration canary"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	s := &preflightStore{}
	client := signedClient(server)
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, client)
	if err != nil {
		t.Fatal(err)
	}
	if s.deleted != 1 {
		t.Fatalf("cleanup=%d", s.deleted)
	}
}

func TestPreflightRejectsPublicCanaryAndCleansUp(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Write([]byte("share receipt migration canary"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("public"))
	}))
	defer server.Close()

	s := &preflightStore{}
	client := signedClient(server)
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, client)
	if err == nil || !strings.Contains(err.Error(), "publicly readable") || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestPreflightRejectsRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Write([]byte("share receipt migration canary"))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	s := &preflightStore{}
	client := signedClient(server)
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, client)
	if err == nil || !strings.Contains(err.Error(), "inconclusive status 302") || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestPreflightRejectsUnexpectedUnsignedStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Write([]byte("share receipt migration canary"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := &preflightStore{}
	client := signedClient(server)
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, client)
	if err == nil || !strings.Contains(err.Error(), "inconclusive status 500") || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestPreflightCleansUpOnPresignFailure(t *testing.T) {
	s := &preflightStore{}
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{fail: true}, http.DefaultClient)
	if err == nil || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestPreflightCleansUpOnCanaryReadFailure(t *testing.T) {
	s := &preflightStore{getErr: errors.New("read failed")}
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, http.DefaultClient)
	if err == nil || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestPreflightCleanupFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Write([]byte("share receipt migration canary"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	s := &preflightStore{deleteErr: errors.New("delete failed")}
	client := signedClient(server)
	err := preflightWithHTTP(context.Background(), testConfig(), fakeOps{}, s, fakeSigner{url: signedURL("/signed?signature=hidden")}, client)
	if err == nil || !strings.Contains(err.Error(), "canary cleanup failed") || s.deleted != 1 {
		t.Fatalf("err=%v cleanup=%d", err, s.deleted)
	}
}

func TestValidatePresignedURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"ok", "https://presigned.test/path", ""},
		{"http", "http://presigned.test/path", "must use HTTPS"},
		{"wrong host", "https://other.test/path", "host mismatch"},
		{"port", "https://presigned.test:8443/path", "must not include a port"},
		{"user", "https://user@presigned.test/path", "user info"},
		{"fragment", "https://presigned.test/path#frag", "fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			err = validatePresignedURL(u, fakeHost)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want %q", err, tc.wantErr)
			}
		})
	}
}

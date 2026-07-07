package auth

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	const cf = "CF-Connecting-IP"

	tests := []struct {
		name          string
		trustedProxy  bool
		realIPHeader  string
		cfHeader      string // value of CF-Connecting-For / configured header
		xff           string
		remoteAddr    string
		want          string
	}{
		{
			name:         "real ip header preferred when trusted",
			trustedProxy: true,
			realIPHeader: cf,
			cfHeader:     "203.0.113.10",
			xff:          "198.51.100.1, 10.0.0.1",
			remoteAddr:   "10.0.0.2:40001",
			want:         "203.0.113.10",
		},
		{
			name:         "real ip header takes first entry if comma-listed",
			trustedProxy: true,
			realIPHeader: cf,
			cfHeader:     "203.0.113.10, 198.51.100.1",
			remoteAddr:   "10.0.0.2:40001",
			want:         "203.0.113.10",
		},
		{
			name:         "real ip header ignored when not trusted (dev)",
			trustedProxy: false,
			realIPHeader: cf,
			cfHeader:     "203.0.113.10",
			remoteAddr:   "192.168.1.5:40001",
			want:         "192.168.1.5",
		},
		{
			name:         "empty real ip header falls back to xff last hop",
			trustedProxy: true,
			realIPHeader: "",
			xff:          "203.0.113.10, 10.0.0.1",
			remoteAddr:   "10.0.0.2:40001",
			want:         "10.0.0.1",
		},
		{
			name:         "real ip header absent falls back to xff last hop",
			trustedProxy: true,
			realIPHeader: cf,
			xff:          "203.0.113.10, 10.0.0.1",
			remoteAddr:   "10.0.0.2:40001",
			want:         "10.0.0.1",
		},
		{
			name:         "no headers falls back to remote addr",
			trustedProxy: true,
			realIPHeader: cf,
			remoteAddr:   "10.0.0.2:40001",
			want:         "10.0.0.2",
		},
		{
			name:         "not trusted uses remote addr directly",
			trustedProxy: false,
			realIPHeader: "",
			xff:          "203.0.113.10",
			remoteAddr:   "192.168.1.5:40001",
			want:         "192.168.1.5",
		},
		{
			name:         "malformed remote addr returned as-is",
			trustedProxy: false,
			remoteAddr:   "bad-addr",
			want:         "bad-addr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{RemoteAddr: tt.remoteAddr, Header: http.Header{}}
			if tt.cfHeader != "" {
				req.Header.Set(cf, tt.cfHeader)
			}
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := ClientIP(req, tt.trustedProxy, tt.realIPHeader)
			if got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
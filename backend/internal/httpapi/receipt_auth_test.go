package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseReceiptURI(t *testing.T) {
	tests := []struct {
		uri, owner, token string
		ok                bool
	}{
		{"/api/sessions/bill-1/receipt", "bill-1", "", true},
		{"/api/view/bv_token/receipt", "", "bv_token", true},
		{"/api/sessions/550e8400-e29b-41d4-a716-446655440000/receipt", "550e8400-e29b-41d4-a716-446655440000", "", true},
		{"/api/view/bv_AbC123_-token/receipt", "", "bv_AbC123_-token", true},
		{"/api/sessions/bill-1/receipt?x=1", "", "", false},
		{"/api/sessions/bill-1/receipt/extra", "", "", false},
		{"/api/sessions/bill%2Fchild/receipt", "", "", false},
		{"/api/view/bv_%2Ftoken/receipt", "", "", false},
		{"/api/sessions/bill-1%2Freceipt/receipt", "", "", false},
		{"/api/view//receipt", "", "", false},
		{"/api/sessions/bill-1/receipt/", "", "", false},
	}
	for _, tt := range tests {
		owner, token, ok := parseReceiptURI(tt.uri)
		if owner != tt.owner || token != tt.token || ok != tt.ok {
			t.Errorf("parseReceiptURI(%q) = %q, %q, %v", tt.uri, owner, token, ok)
		}
	}
}

func TestReceiptAuthorizationRejectsMalformedForwardedRequest(t *testing.T) {
	for _, tt := range []struct {
		method, uri string
	}{
		{"POST", "/api/view/bv_token/receipt"},
		{"GET", "/api/sessions/bill-1/other"},
		{"HEAD", "/api/sessions/bill-1/receipt?object=attacker"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/internal/receipts/authorize", nil)
		r.Header.Set("X-Forwarded-Method", tt.method)
		r.Header.Set("X-Forwarded-Uri", tt.uri)
		recorder := httptest.NewRecorder()
		(&Server{}).handleReceiptAuthorization(recorder, r)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tt.method, tt.uri, recorder.Code)
		}
		if got := recorder.Header().Get("X-Share-S3-URI"); got != "" {
			t.Errorf("%s %s: leaked S3 URI %q", tt.method, tt.uri, got)
		}
	}
}

func TestValidS3ProxyURI(t *testing.T) {
	good, ok := validS3ProxyURI("https://share-app.s3.bhs.io.cloud.ovh.net/receipts/a.jpg?X-Amz-Signature=x", "share-app.s3.bhs.io.cloud.ovh.net")
	if !ok || good != "/receipts/a.jpg?X-Amz-Signature=x" {
		t.Fatalf("valid URL rejected or URI changed: %q, %v", good, ok)
	}
	for _, raw := range []string{
		"http://share-app.s3.bhs.io.cloud.ovh.net/a",
		"https://other.example/a",
		"https://share-app.s3.bhs.io.cloud.ovh.net:443/a",
		"https://share-app.s3.bhs.io.cloud.ovh.net",
	} {
		if _, ok := validS3ProxyURI(raw, "share-app.s3.bhs.io.cloud.ovh.net"); ok {
			t.Errorf("accepted invalid presigned URL %q", raw)
		}
	}
}

func TestReceiptMethodAllowed(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		if !receiptMethodAllowed(method) {
			t.Errorf("rejected %s", method)
		}
	}
	for _, method := range []string{"", "POST", "PUT", "OPTIONS"} {
		if receiptMethodAllowed(method) {
			t.Errorf("accepted %s", method)
		}
	}
}

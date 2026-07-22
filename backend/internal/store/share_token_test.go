package store

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestNewShareTokenIsRandomAndLookupHashMatches(t *testing.T) {
	first, firstHash, err := newShareToken()
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := newShareToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("generated share tokens should not be reused")
	}
	if !strings.HasPrefix(first, "bv_") || !strings.HasPrefix(second, "bv_") {
		t.Fatalf("tokens missing bv_ prefix: %q, %q", first, second)
	}
	want := sha256.Sum256([]byte(first))
	if string(firstHash) != string(want[:]) {
		t.Fatal("lookup hash does not match plaintext token")
	}
	if string(firstHash) == string(secondHash) {
		t.Fatal("different tokens should have different hashes")
	}
}

func TestShareLinkForStoredToken(t *testing.T) {
	token := "bv_existing"
	hash := []byte("legacy-hash")

	available, exists := shareLinkForStoredToken(&token, hash)
	if !exists || !available.Exists || !available.Available || available.Token == nil || *available.Token != token {
		t.Fatalf("plaintext link = %+v, exists = %v", available, exists)
	}

	legacy, exists := shareLinkForStoredToken(nil, hash)
	if !exists || !legacy.Exists || legacy.Available || legacy.Token != nil {
		t.Fatalf("legacy link = %+v, exists = %v", legacy, exists)
	}

	missing, exists := shareLinkForStoredToken(nil, nil)
	if exists || missing.Exists || missing.Available || missing.Token != nil {
		t.Fatalf("missing link = %+v, exists = %v", missing, exists)
	}
}

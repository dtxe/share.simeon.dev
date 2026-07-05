// Package cleanup runs a periodic sweep of expired sessions, OTP codes,
// WebAuthn ceremonies, and stale bills — including deleting the
// now-orphaned receipt image files from disk, not just the DB rows.
package cleanup

import (
	"context"
	"log"
	"time"

	"share/backend/internal/receipts"
	"share/backend/internal/store"
)

const interval = 1 * time.Hour

// Run blocks, sweeping immediately and then on every tick, until ctx is done.
func Run(ctx context.Context, s *store.Store, rs *receipts.Storage) {
	sweep(ctx, s, rs)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, s, rs)
		}
	}
}

func sweep(ctx context.Context, s *store.Store, rs *receipts.Storage) {
	if n, err := s.DeleteExpiredSessions(ctx); err != nil {
		log.Printf("cleanup: sessions: %v", err)
	} else if n > 0 {
		log.Printf("cleanup: removed %d expired sessions", n)
	}

	if n, err := s.DeleteExpiredOTPCodes(ctx); err != nil {
		log.Printf("cleanup: otp codes: %v", err)
	} else if n > 0 {
		log.Printf("cleanup: removed %d expired otp codes", n)
	}

	if n, err := s.DeleteExpiredWebauthnCeremonies(ctx); err != nil {
		log.Printf("cleanup: webauthn ceremonies: %v", err)
	} else if n > 0 {
		log.Printf("cleanup: removed %d expired webauthn ceremonies", n)
	}

	receiptPaths, n, err := s.DeleteExpiredBillSessions(ctx)
	if err != nil {
		log.Printf("cleanup: bill sessions: %v", err)
	} else if n > 0 {
		log.Printf("cleanup: removed %d expired bill sessions", n)
	}
	for _, p := range receiptPaths {
		if err := rs.Delete(p); err != nil {
			log.Printf("cleanup: removing receipt file %q: %v", p, err)
		}
	}
}

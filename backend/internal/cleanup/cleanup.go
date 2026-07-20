// Package cleanup runs a periodic sweep of expired sessions, OTP codes,
// WebAuthn ceremonies, and stale bills — including processing receipt objects
// through the durable deletion queue.
package cleanup

import (
	"context"
	"log"
	"time"

	"share/backend/internal/receipts"
	"share/backend/internal/store"
)

const interval = 1 * time.Hour
const itemTimeout = 5 * time.Second

type cleanupStore interface {
	DeleteExpiredSessions(context.Context) (int64, error)
	DeleteExpiredOTPCodes(context.Context) (int64, error)
	DeleteExpiredWebauthnCeremonies(context.Context) (int64, error)
	DeleteExpiredBillSessions(context.Context) (int64, error)
	ClaimReceiptDeletions(context.Context, int) ([]store.ReceiptDeletion, error)
	AckReceiptDeletion(context.Context, int64) error
	RetryReceiptDeletion(context.Context, int64) error
}

// Run blocks, sweeping immediately and then on every tick, until ctx is done.
func Run(ctx context.Context, s *store.Store, rs receipts.ReceiptStorage) {
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

func sweep(ctx context.Context, s cleanupStore, rs receipts.ReceiptStorage) {
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

	n, err := s.DeleteExpiredBillSessions(ctx)
	if err != nil {
		log.Printf("cleanup: bill sessions: %v", err)
	} else if n > 0 {
		log.Printf("cleanup: removed %d expired bill sessions", n)
	}
	processQueue(ctx, s, rs)
}

func processQueue(ctx context.Context, s cleanupStore, rs receipts.ReceiptStorage) {
	items, err := s.ClaimReceiptDeletions(ctx, 100)
	if err != nil {
		log.Printf("cleanup: receipt queue: %v", err)
		return
	}
	for _, item := range items {
		deleteCtx, deleteCancel := context.WithTimeout(context.WithoutCancel(ctx), itemTimeout)
		err := rs.Delete(deleteCtx, item.Path)
		deleteCancel()
		if err != nil {
			retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(ctx), itemTimeout)
			retryErr := s.RetryReceiptDeletion(retryCtx, item.ID)
			retryCancel()
			if retryErr != nil {
				log.Printf("cleanup: receipt retry: %v", retryErr)
			}
			continue
		}
		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), itemTimeout)
		err = s.AckReceiptDeletion(ackCtx, item.ID)
		ackCancel()
		if err != nil {
			log.Printf("cleanup: receipt ack: %v", err)
		}
	}
}

package ratelimit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// These run against a real Redis (set REDIS_URL, e.g. via `docker compose
// exec backend go test ./internal/ratelimit/...`) rather than a fake —
// fixed-window expiry semantics are exactly the kind of thing that looks
// right against a mock and behaves differently against the real server.
func newTestLimiter(t *testing.T) *Limiter {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; run inside the backend container to test against real Redis")
	}
	l, err := New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return l
}

func uniqueKeySuffix() string {
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

func TestAllowWindowResets(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	key := "test:window:" + uniqueKeySuffix()

	// Redis EXPIRE has whole-second granularity, so the window here must be
	// at least 1s regardless of how short we'd like the test to be.
	const window = 1 * time.Second

	for i := 0; i < 3; i++ {
		ok, err := l.Allow(ctx, key, 3, window)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !ok {
			t.Fatalf("expected allow on attempt %d", i)
		}
	}

	ok, err := l.Allow(ctx, key, 3, window)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatalf("expected 4th attempt within window to be blocked")
	}

	time.Sleep(window + 300*time.Millisecond)

	ok, err = l.Allow(ctx, key, 3, window)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Fatalf("expected window to have reset after expiry")
	}
}

func TestReserveLLMSpendRollsBackRejection(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()

	// The spend counter is a single global-per-day key, so a prior test (or
	// prior run today) may have already left it non-zero — peek first and
	// set the capCents relative to that, rather than assuming it starts at 0.
	before, err := l.PeekLLMSpend(ctx)
	if err != nil {
		t.Fatalf("PeekLLMSpend: %v", err)
	}
	capCents := before + 500
	// The spend key is shared with real traffic (it's keyed by date, not by
	// test) — leave it exactly as we found it.
	t.Cleanup(func() {
		if err := l.AdjustLLMSpend(context.Background(), -500); err != nil {
			t.Logf("cleanup: failed to revert test spend delta: %v", err)
		}
	})

	ok, err := l.ReserveLLMSpend(ctx, 500, capCents)
	if err != nil {
		t.Fatalf("ReserveLLMSpend: %v", err)
	}
	if !ok {
		t.Fatalf("expected the reservation that lands exactly at the capCents to be accepted")
	}

	ok2, err := l.ReserveLLMSpend(ctx, 1, capCents)
	if err != nil {
		t.Fatalf("ReserveLLMSpend: %v", err)
	}
	if ok2 {
		t.Fatalf("expected a reservation pushing total past the capCents to be rejected")
	}

	// Rejected reservation must have been rolled back: spending exactly up
	// to the remaining headroom (0 more) should still succeed at capCents.
	ok3, err := l.ReserveLLMSpend(ctx, 0, capCents)
	if err != nil {
		t.Fatalf("ReserveLLMSpend: %v", err)
	}
	if !ok3 {
		t.Fatalf("expected total to still be exactly at capCents (rollback of prior rejection didn't happen)")
	}
}

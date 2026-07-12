package ratelimit

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"
)

// These run against a real Redis (set REDIS_URL, e.g. via `docker compose
// exec backend go test ./internal/ratelimit/...`) rather than a fake —
// fixed-window expiry semantics and Lua atomicity are exactly the kind of
// thing that looks right against a mock and behaves differently against the
// real server.
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

// isolatedDateKey returns a unique dateKey so each test gets its own Redis
// keys and never collides with other tests or real traffic.
func isolatedDateKey() string {
	return fmt.Sprintf("test.%d.%d", os.Getpid(), rand.Int63())
}

func uniqueKeySuffix() string {
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

// ---------------------------------------------------------------------------
// Boundary rejection — fill exactly to cap, next reservation is rejected.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_BoundaryRejection(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const capCents = 100

	// Fill up to exactly the cap: 5 reservations of 20 each.
	for i := 0; i < 5; i++ {
		res, ok, err := l.reserveLLMSpend(ctx, dateKey, 20, capCents, true)
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("reserve %d: expected acceptance within cap", i)
		}
		if res.ReservedCents != 20 {
			t.Fatalf("reserve %d: ReservedCents = %d, want 20", i, res.ReservedCents)
		}
	}

	// One more — must be rejected (100 + 1 > 100).
	_, ok, err := l.reserveLLMSpend(ctx, dateKey, 1, capCents, true)
	if err != nil {
		t.Fatalf("reserve beyond cap: %v", err)
	}
	if ok {
		t.Fatal("expected reservation beyond cap to be rejected")
	}

	// The daily total must still be exactly capCents (rejection didn't
	// leak).
	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}
	if v != capCents {
		t.Fatalf("daily total = %d, want %d", v, capCents)
	}
}

// ---------------------------------------------------------------------------
// Concurrent admission — N goroutines race to fill the cap, total never
// exceeds it.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_ConcurrentAdmission(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const (
		goroutines = 20
		amount     = 10
		capCents   = 80 // exactly 8 of 20 goroutines can win
	)

	var wg sync.WaitGroup
	accepted := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := l.reserveLLMSpend(ctx, dateKey, amount, capCents, false)
			if err != nil {
				t.Errorf("concurrent reserve: %v", err)
				accepted <- false
				return
			}
			accepted <- ok
		}()
	}
	wg.Wait()
	close(accepted)

	acceptedCount := 0
	for ok := range accepted {
		if ok {
			acceptedCount++
		}
	}

	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}

	if acceptedCount != capCents/amount {
		t.Fatalf("accepted %d reservations, want %d (cap %d / amount %d)",
			acceptedCount, capCents/amount, capCents, amount)
	}
	if v != capCents {
		t.Fatalf("daily total = %d, want %d", v, capCents)
	}
}

// ---------------------------------------------------------------------------
// Idempotent concurrent finalization — many goroutines finalize the same
// reservation concurrently; only the first has any effect.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_IdempotentConcurrentFinalization(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const capCents = 500

	res, ok, err := l.reserveLLMSpend(ctx, dateKey, 100, capCents, true)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !ok {
		t.Fatal("expected reservation accepted")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := l.FinalizeLLMSpend(ctx, res, 40)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("FinalizeLLMSpend: %v", err)
		}
	}

	// After finalization, the reservation key must be gone.
	_, prefix := dailyKeys(dateKey)
	resvKey := prefix + res.ReservationID
	exists, err := l.rdb.Exists(ctx, resvKey).Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists != 0 {
		t.Fatal("expected reservation key to be deleted after finalization")
	}

	// The reservation is reconciled from 100 cents to 40 cents exactly once.
	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}
	if v != 40 {
		t.Fatalf("daily total = %d, want 40", v)
	}

	// Finalize again on the already-deleted reservation — must still be a
	// no-op, not an error.
	if finalized, err := l.FinalizeLLMSpend(ctx, res, 90); err != nil {
		t.Fatalf("second FinalizeLLMSpend: %v", err)
	} else if finalized {
		t.Fatal("second FinalizeLLMSpend unexpectedly finalized again")
	}
}

// ---------------------------------------------------------------------------
// Release / overage — abandoned reservations remain counted until expiry.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_ReleaseOrphanStayCounted(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const capCents = 100

	// Two reservations, each 50.  Together they exactly fill the cap.
	_, ok, err := l.reserveLLMSpend(ctx, dateKey, 50, capCents, true)
	if err != nil {
		t.Fatalf("reserve r1: %v", err)
	}
	if !ok {
		t.Fatal("expected r1 accepted")
	}

	r2, ok, err := l.reserveLLMSpend(ctx, dateKey, 50, capCents, true)
	if err != nil {
		t.Fatalf("reserve r2: %v", err)
	}
	if !ok {
		t.Fatal("expected r2 accepted")
	}

	// Finalize r2 but forget r1 (abandon).  The total must still be 100.
	if _, err := l.FinalizeLLMSpend(ctx, r2, 50); err != nil {
		t.Fatalf("finalize r2: %v", err)
	}

	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}
	if v != 100 {
		t.Fatalf("daily total after abandoning r1 = %d, want 100", v)
	}

	// Peek should also report 100.
	// (We can't call PeekLLMSpend directly because it uses today's date,
	// not dateKey.  Read the key manually.)
	v2, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("peek daily total: %v", err)
	}
	if v2 != 100 {
		t.Fatalf("peek = %d, want 100", v2)
	}
}

// ---------------------------------------------------------------------------
// Exact-key finalization — Finalize must use exactly the same date key and
// reservation ID that Reserve returned.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_ExactKeyFinalization(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const capCents = 500

	res, ok, err := l.reserveLLMSpend(ctx, dateKey, 75, capCents, true)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !ok {
		t.Fatal("expected reservation accepted")
	}

	if res.DateKey != dateKey {
		t.Fatalf("SpendReservation.DateKey = %q, want %q", res.DateKey, dateKey)
	}
	if res.ReservationID == "" {
		t.Fatal("SpendReservation.ReservationID must not be empty")
	}
	if res.ReservedCents != 75 {
		t.Fatalf("SpendReservation.ReservedCents = %d, want 75", res.ReservedCents)
	}

	// Finalize using the exact reservation — must succeed.
	if _, err := l.FinalizeLLMSpend(ctx, res, 75); err != nil {
		t.Fatalf("FinalizeLLMSpend: %v", err)
	}

	// Verify the reservation key is gone.
	_, prefix := dailyKeys(dateKey)
	resvKey := prefix + res.ReservationID
	exists, err := l.rdb.Exists(ctx, resvKey).Result()
	if err != nil {
		t.Fatalf("Exists after finalize: %v", err)
	}
	if exists != 0 {
		t.Fatal("reservation key still exists after finalization")
	}
}

func TestFinalizeLLMSpendReconcilesReleaseAndOverage(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	released, ok, err := l.reserveLLMSpend(ctx, dateKey, 4, 100, true)
	if err != nil || !ok {
		t.Fatalf("reserve release case: ok=%v err=%v", ok, err)
	}
	if _, err := l.FinalizeLLMSpend(ctx, released, 1); err != nil {
		t.Fatalf("finalize release case: %v", err)
	}

	overage, ok, err := l.reserveLLMSpend(ctx, dateKey, 4, 100, true)
	if err != nil || !ok {
		t.Fatalf("reserve overage case: ok=%v err=%v", ok, err)
	}
	if _, err := l.FinalizeLLMSpend(ctx, overage, 7); err != nil {
		t.Fatalf("finalize overage case: %v", err)
	}

	dailyKey, _ := dailyKeys(dateKey)
	got, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}
	if got != 8 {
		t.Fatalf("daily total = %d, want 8", got)
	}
}

// ---------------------------------------------------------------------------
// Nil reservation guard — FinalizeLLMSpend must reject nil.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_NilReservation(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()

	_, err := l.FinalizeLLMSpend(ctx, nil, 0)
	if err == nil {
		t.Fatal("expected error for nil reservation")
	}
}

// ---------------------------------------------------------------------------
// Zero-amount reservation — must succeed if cap allows.
// ---------------------------------------------------------------------------

func TestReserveLLMSpend_ZeroAmount(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	dateKey := isolatedDateKey()

	const capCents = 100

	res, ok, err := l.reserveLLMSpend(ctx, dateKey, 0, capCents, true)
	if err != nil {
		t.Fatalf("reserve 0: %v", err)
	}
	if !ok {
		t.Fatal("expected 0-cent reservation accepted")
	}
	if res.ReservedCents != 0 {
		t.Fatalf("ReservedCents = %d, want 0", res.ReservedCents)
	}

	// The daily total must still be 0 — incrementing by 0 does nothing.
	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		t.Fatalf("get daily total: %v", err)
	}
	if v != 0 {
		t.Fatalf("daily total after 0-cent reserve = %d, want 0", v)
	}
}

// ---------------------------------------------------------------------------
// PeekLLMSpend — must still work after the refactor (uses new key format
// internally).
// ---------------------------------------------------------------------------

func TestPeekLLMSpend(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()

	// Peek on an empty daily bucket must return 0.
	v, err := l.PeekLLMSpend(ctx)
	if err != nil {
		t.Fatalf("PeekLLMSpend on empty: %v", err)
	}
	// We can't assert v == 0 because other tests or real traffic may have
	// touched today's real key.  Just verify the method returns without
	// error and the value is non-negative.
	if v < 0 {
		t.Fatalf("PeekLLMSpend returned negative: %d", v)
	}
}

// ---------------------------------------------------------------------------
// Fixed-window rate limiter — existing test preserved as-is.
// ---------------------------------------------------------------------------

func TestAllowWindowResets(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	key := "test:window:" + uniqueKeySuffix()

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

// Package ratelimit implements per-IP fixed-window request counters and the
// global LLM daily spend cap, both backed by Redis. This is the only place
// in the app that touches Redis — everything durable (users, bills,
// sessions, OTP codes) lives in Postgres.
package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb *redis.Client
}

func New(redisURL string) (*Limiter, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: parsing REDIS_URL: %w", err)
	}
	return &Limiter{rdb: redis.NewClient(opts)}, nil
}

func (l *Limiter) Ping(ctx context.Context) error {
	return l.rdb.Ping(ctx).Err()
}

// Allow implements a fixed-window counter: the first hit in a window sets
// its expiry, subsequent hits just increment. Returns true while the count
// is at or under max.
func (l *Limiter) Allow(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := l.rdb.Expire(ctx, key, window).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(max), nil
}

func (l *Limiter) AllowGlobalPerIP(ctx context.Context, ip string) (bool, error) {
	return l.Allow(ctx, "rl:global:"+ip, 60, time.Minute)
}

func (l *Limiter) AllowCreateSessionPerIP(ctx context.Context, ip string) (bool, error) {
	return l.Allow(ctx, "rl:create_session:"+ip, 10, time.Hour)
}

func (l *Limiter) AllowExtractPerIP(ctx context.Context, ip string) (bool, error) {
	allowed, _, _, err := l.AllowExtractPerIPDetailed(ctx, ip)
	return allowed, err
}

// ExtractionWindow identifies the independent extraction cap that rejected a
// request. All windows are checked; a request is allowed only when all pass.
type ExtractionWindow struct {
	Name      string
	Threshold int
	Duration  time.Duration
}

var extractionWindows = []ExtractionWindow{
	{Name: "hour", Threshold: 10, Duration: time.Hour},
	{Name: "day", Threshold: 20, Duration: 24 * time.Hour},
	{Name: "month", Threshold: 50, Duration: 30 * 24 * time.Hour},
}

// AllowExtractPerIPDetailed applies the production extraction caps using
// independent fixed-window Redis counters and reports the exhausted window.
func (l *Limiter) AllowExtractPerIPDetailed(ctx context.Context, ip string) (bool, string, int, error) {
	allowedAll := true
	var exhaustedName string
	var exhaustedThreshold int
	for _, window := range extractionWindows {
		key := "rl:extract:ip:" + window.Name + ":" + ip
		allowed, err := l.Allow(ctx, key, window.Threshold, window.Duration)
		if err != nil {
			return false, "", 0, err
		}
		if !allowed {
			allowedAll = false
			if exhaustedName == "" {
				exhaustedName = window.Name
				exhaustedThreshold = window.Threshold
			}
		}
	}
	return allowedAll, exhaustedName, exhaustedThreshold, nil
}

func (l *Limiter) AllowOTPRequestPerIP(ctx context.Context, ip string, maxPerHour int) (bool, error) {
	return l.Allow(ctx, "rl:otp_request:"+ip, maxPerHour, time.Hour)
}

func (l *Limiter) AllowOTPVerifyPerIP(ctx context.Context, ip string, maxPerHour int) (bool, error) {
	return l.Allow(ctx, "rl:otp_verify:"+ip, maxPerHour, time.Hour)
}

func (l *Limiter) AllowInvalidViewTokenPerIP(ctx context.Context, ip string) (bool, error) {
	return l.Allow(ctx, "rl:bad_view_token:"+ip, 30, time.Minute)
}

const (
	reservationTTL = 25 * time.Hour
	resvTTLsecs    = int64(reservationTTL / time.Second)
)

// SpendReservation represents an accepted LLM spend reservation that can be
// finalized against the same daily bucket even if UTC midnight passes.
type SpendReservation struct {
	ReservationID string
	DateKey       string // daily bucket identifier, e.g. "2026-07-12"
	ReservedCents int
}

// reserveLua atomically checks the cap, increments the daily total, and
// records a reservation.
//
// KEYS[1] — daily total key  rl:llm_spend:{date}
// KEYS[2] — reservation key  rl:llm_resv:{date}:<id>
// ARGV[1] — estimatedCents (int string)
// ARGV[2] — capCents        (int string)
// ARGV[3] - ttl seconds
//
// Returns a two-element array: [status (1=ok, 0=rejected), new_total].
var reserveLua = redis.NewScript(`
local daily  = KEYS[1]
local resv   = KEYS[2]
local amount = tonumber(ARGV[1])
local cap    = tonumber(ARGV[2])
local ttl    = tonumber(ARGV[3])

local cur = redis.call('GET', daily)
if not cur then cur = 0 else cur = tonumber(cur) end

if cur + amount > cap then
    return {0, cur}
end

local new_total = redis.call('INCRBY', daily, amount)
if new_total == amount then
    redis.call('EXPIRE', daily, ttl)
end

redis.call('SET', resv, amount, 'EX', ttl)
return {1, new_total}
`)

// finalizeLua atomically replaces the reservation with the accounted cost.
// If another caller already finalized the reservation, it is a no-op.
//
// KEYS[1] — daily total key  rl:llm_spend:{date}
// KEYS[2] — reservation key  rl:llm_resv:{date}:<id>
// ARGV[1] - accounted cents
// ARGV[2] - ttl seconds
//
// Returns 1 on successful finalization, 0 if already finalized (no-op).
var finalizeLua = redis.NewScript(`
local daily  = KEYS[1]
local resv   = KEYS[2]
local accounted = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])

local v = redis.call('GET', resv)
if not v then
    return 0
end
local reserved = tonumber(v)
if redis.call('EXISTS', daily) == 1 then
    redis.call('INCRBY', daily, accounted - reserved)
else
    redis.call('SET', daily, accounted, 'EX', ttl)
end
redis.call('DEL', resv)
return 1
`)

// dailyKeys returns the Redis keys for the given dateKey.  Both keys use the
// same {hash:tag} so Lua scripts run atomically on a single cluster slot.
func dailyKeys(dateKey string) (dailyTotalKey, resvPrefix string) {
	return "rl:llm_spend:{" + dateKey + "}", "rl:llm_resv:{" + dateKey + "}:"
}

func generateReservationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("ratelimit: generate reservation ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// reserveLLMSpend is the shared implementation for both the compat wrapper
// and the full reservation API.  It runs the Lua script and builds the
// SpendReservation when the reservation is accepted.
func (l *Limiter) reserveLLMSpend(ctx context.Context, dateKey string, estimatedCents, capCents int, returnReservation bool) (*SpendReservation, bool, error) {
	if estimatedCents < 0 {
		return nil, false, fmt.Errorf("ratelimit: reservation amount must not be negative")
	}
	if capCents <= 0 {
		return nil, false, fmt.Errorf("ratelimit: spend cap must be positive")
	}
	resvID, err := generateReservationID()
	if err != nil {
		return nil, false, err
	}
	dailyKey, prefix := dailyKeys(dateKey)
	resvKey := prefix + resvID

	vals, err := reserveLua.Run(ctx, l.rdb, []string{dailyKey, resvKey}, estimatedCents, capCents, resvTTLsecs).Result()
	if err != nil {
		return nil, false, err
	}

	arr, ok := vals.([]interface{})
	if !ok || len(arr) < 2 {
		return nil, false, fmt.Errorf("ratelimit: unexpected Lua result type %T", vals)
	}

	status, ok1 := arr[0].(int64)
	if !ok1 {
		return nil, false, fmt.Errorf("ratelimit: unexpected Lua status type %T", arr[0])
	}

	if status == 0 {
		return nil, false, nil // rejected
	}

	if !returnReservation {
		return nil, true, nil // accepted, caller doesn't want reservation object
	}

	return &SpendReservation{
		ReservationID: resvID,
		DateKey:       dateKey,
		ReservedCents: estimatedCents,
	}, true, nil
}

// ReserveLLMSpendDetailed atomically checks the daily spend cap and, if the
// estimatedCents fits under the capCents, records a reservation, increments
// the daily total, and returns a SpendReservation.  The caller MUST call
// FinalizeLLMSpend once the actual cost is known so the reservation metadata
// is cleaned up.  Abandoned reservations expire automatically after ~25 h.
func (l *Limiter) ReserveLLMSpendDetailed(ctx context.Context, estimatedCents, capCents int) (*SpendReservation, bool, error) {
	dateKey := time.Now().UTC().Format("2006-01-02")
	return l.reserveLLMSpend(ctx, dateKey, estimatedCents, capCents, true)
}

// FinalizeLLMSpend atomically reconciles a reservation to accountedCents.
// It returns false without changing the total if the reservation was already
// finalized. Unknown-cost calls should be accounted at the configured
// per-call reservation amount rather than zero.
func (l *Limiter) FinalizeLLMSpend(ctx context.Context, res *SpendReservation, accountedCents int) (bool, error) {
	if res == nil {
		return false, fmt.Errorf("ratelimit: FinalizeLLMSpend called with nil reservation")
	}
	if accountedCents < 0 {
		return false, fmt.Errorf("ratelimit: accounted cost must not be negative")
	}
	dailyKey, prefix := dailyKeys(res.DateKey)
	resvKey := prefix + res.ReservationID

	result, err := finalizeLua.Run(ctx, l.rdb, []string{dailyKey, resvKey}, accountedCents, resvTTLsecs).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// PeekLLMSpend reads today's running total without changing it.
func (l *Limiter) PeekLLMSpend(ctx context.Context) (int, error) {
	dateKey := time.Now().UTC().Format("2006-01-02")
	dailyKey, _ := dailyKeys(dateKey)
	v, err := l.rdb.Get(ctx, dailyKey).Int()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

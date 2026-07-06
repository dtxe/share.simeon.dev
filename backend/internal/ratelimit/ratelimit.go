// Package ratelimit implements per-IP fixed-window request counters and the
// global LLM daily spend cap, both backed by Redis. This is the only place
// in the app that touches Redis — everything durable (users, bills,
// sessions, OTP codes) lives in Postgres.
package ratelimit

import (
	"context"
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
	return l.Allow(ctx, "rl:extract:"+ip, 5, time.Hour)
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

// ReserveLLMSpend atomically adds estimatedCents to today's running total
// and reports whether that keeps the day under capCents. If it doesn't, the
// reservation is rolled back (so a rejected call never counts against the
// cap) and the caller should refuse the request. Key self-expires after
// ~25h so it resets daily without a cron job.
func (l *Limiter) ReserveLLMSpend(ctx context.Context, estimatedCents, capCents int) (bool, error) {
	key := "rl:llm_spend:" + time.Now().UTC().Format("2006-01-02")

	total, err := l.rdb.IncrBy(ctx, key, int64(estimatedCents)).Result()
	if err != nil {
		return false, err
	}
	if total == int64(estimatedCents) {
		if err := l.rdb.Expire(ctx, key, 25*time.Hour).Err(); err != nil {
			return false, err
		}
	}
	if total > int64(capCents) {
		// Roll back this reservation — it wasn't actually spent.
		_, _ = l.rdb.DecrBy(ctx, key, int64(estimatedCents)).Result()
		return false, nil
	}
	return true, nil
}

// PeekLLMSpend reads today's running total without changing it.
func (l *Limiter) PeekLLMSpend(ctx context.Context) (int, error) {
	key := "rl:llm_spend:" + time.Now().UTC().Format("2006-01-02")
	v, err := l.rdb.Get(ctx, key).Int()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

// AdjustLLMSpend corrects a prior estimate once the real cost is known
// (e.g. from the provider's reported token usage). deltaCents may be
// negative if the estimate overshot.
func (l *Limiter) AdjustLLMSpend(ctx context.Context, deltaCents int) error {
	key := "rl:llm_spend:" + time.Now().UTC().Format("2006-01-02")
	return l.rdb.IncrBy(ctx, key, int64(deltaCents)).Err()
}

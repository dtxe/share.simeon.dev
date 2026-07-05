package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"share/backend/internal/email"
)

var (
	ErrOTPCooldown     = errors.New("auth: resend cooldown active")
	ErrOTPInvalid      = errors.New("auth: invalid or expired code")
	ErrOTPTooManyTries = errors.New("auth: too many attempts, request a new code")
)

// RequestOTP generates and emails a 6-digit code for `email`, enforcing a
// per-address resend cooldown (per-IP request rate limiting is handled
// separately by internal/ratelimit in front of the HTTP handler).
func (m *Manager) RequestOTP(ctx context.Context, provider email.Provider, to string) error {
	var lastCreated time.Time
	err := m.pool.QueryRow(ctx, `
		SELECT created_at FROM otp_codes WHERE email = $1 ORDER BY created_at DESC LIMIT 1
	`, to).Scan(&lastCreated)
	if err == nil {
		cooldown := time.Duration(m.cfg.OTPResendCooldownSeconds) * time.Second
		if time.Since(lastCreated) < cooldown {
			return ErrOTPCooldown
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	code, err := generateOTPCode()
	if err != nil {
		return err
	}
	hash := hashOTP(to, code)
	expiresAt := time.Now().Add(time.Duration(m.cfg.OTPCodeTTLSeconds) * time.Second)

	if _, err := m.pool.Exec(ctx, `
		INSERT INTO otp_codes (email, code_hash, expires_at)
		VALUES ($1, $2, $3)
	`, to, hash, expiresAt); err != nil {
		return err
	}

	return provider.SendOTP(ctx, to, code)
}

// VerifyOTP checks `code` against the most recent unconsumed code for
// `email`. On success it attaches/merges the email onto currentUserID (see
// merge.go) and returns the caller's resulting identity.
func (m *Manager) VerifyOTP(ctx context.Context, currentUserID, to, code string) (finalUserID string, err error) {
	var (
		id       string
		codeHash []byte
		attempts int
	)
	err = m.pool.QueryRow(ctx, `
		SELECT id::text, code_hash, attempts
		FROM otp_codes
		WHERE email = $1 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC LIMIT 1
	`, to).Scan(&id, &codeHash, &attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrOTPInvalid
		}
		return "", err
	}

	if attempts >= m.cfg.OTPMaxAttempts {
		_, _ = m.pool.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, id)
		return "", ErrOTPTooManyTries
	}

	want := hashOTP(to, code)
	if subtle.ConstantTimeCompare(want, codeHash) != 1 {
		_, _ = m.pool.Exec(ctx, `UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1`, id)
		return "", ErrOTPInvalid
	}

	if _, err := m.pool.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, id); err != nil {
		return "", err
	}

	return m.AttachEmailOrMerge(ctx, currentUserID, to)
}

func generateOTPCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

// hashOTP salts the code with the email so the same 6-digit code sent to two
// different addresses doesn't hash identically.
func hashOTP(email, code string) []byte {
	sum := sha256.Sum256([]byte(email + ":" + code))
	return sum[:]
}

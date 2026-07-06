package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
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
	hash := hashOTP(to, code, m.cfg.OTPHashPepper)
	expiresAt := time.Now().Add(time.Duration(m.cfg.OTPCodeTTLSeconds) * time.Second)

	// Invalidate any still-live codes for this address before issuing a new
	// one, so an older code can't be guessed after a fresher one is sent.
	if _, err := m.pool.Exec(ctx, `
		UPDATE otp_codes SET consumed_at = now() WHERE email = $1 AND consumed_at IS NULL
	`, to); err != nil {
		return err
	}

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
// merge.go), rotates the session (kills the pre-auth token, issues and
// writes a fresh one for the resulting identity so a fixed/stolen
// anonymous token can't ride along as an authenticated session), and
// returns the caller's resulting identity.
func (m *Manager) VerifyOTP(ctx context.Context, w http.ResponseWriter, r *http.Request, currentUserID, to, code string) (finalUserID string, err error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var (
		id       string
		codeHash []byte
		attempts int
	)
	// FOR UPDATE serializes concurrent verify attempts against the same
	// code row, so racing requests can't both pass the attempts check or
	// both consume the same code.
	err = tx.QueryRow(ctx, `
		SELECT id::text, code_hash, attempts
		FROM otp_codes
		WHERE email = $1 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC LIMIT 1
		FOR UPDATE
	`, to).Scan(&id, &codeHash, &attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrOTPInvalid
		}
		return "", err
	}

	if attempts >= m.cfg.OTPMaxAttempts {
		if _, err := tx.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, id); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrOTPTooManyTries
	}

	want := hashOTP(to, code, m.cfg.OTPHashPepper)
	if subtle.ConstantTimeCompare(want, codeHash) != 1 {
		if _, err := tx.Exec(ctx, `UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1`, id); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrOTPInvalid
	}

	if _, err := tx.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	finalUserID, err = m.AttachEmailOrMerge(ctx, currentUserID, to)
	if err != nil {
		return "", err
	}

	// Rotate: kill whatever session made this request and issue a brand
	// new one for the final identity.
	if raw, ok := m.readToken(r); ok {
		_, _ = m.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(raw))
	}
	newRaw, _, err := m.CreateSessionFor(ctx, r, finalUserID)
	if err != nil {
		return "", err
	}
	m.writeToken(w, newRaw)
	if m.cfg.AnonIdentityTransport == "header" {
		w.Header().Set(m.cfg.AnonSessionHeaderName, newRaw)
	}
	return finalUserID, nil
}

func generateOTPCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

// hashOTP keys the short OTP with a server-side pepper so a DB-only leak does
// not allow offline brute force of the 6-digit code space.
func hashOTP(email, code, pepper string) []byte {
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(email + ":" + code))
	return mac.Sum(nil)
}

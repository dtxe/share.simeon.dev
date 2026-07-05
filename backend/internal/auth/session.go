package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cher-app/backend/internal/config"
)

var ErrNoSession = errors.New("auth: no valid session")

type Session struct {
	ID       string
	UserID   string
	ExpireAt time.Time
	LastSeen time.Time
}

type Manager struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	webauthn *webauthn.WebAuthn
}

// NewManager builds the auth Manager. If passkeys are enabled but the
// WebAuthn config is invalid (e.g. a malformed origin URL), that's a startup
// configuration error, not something to silently degrade.
func NewManager(pool *pgxpool.Pool, cfg *config.Config) (*Manager, error) {
	m := &Manager{pool: pool, cfg: cfg}
	if cfg.PasskeyAccountsEnabled {
		w, err := newWebAuthn(cfg)
		if err != nil {
			return nil, fmt.Errorf("auth: webauthn config: %w", err)
		}
		m.webauthn = w
	}
	return m, nil
}

// generateToken returns the raw token (to hand to the client) and its sha256
// hash (what's stored/looked-up server-side). 256 bits of crypto/rand.
func generateToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// createAnonymousSession provisions a brand-new user (no email) and a
// session for them.
func (m *Manager) createAnonymousSession(ctx context.Context, r *http.Request) (raw string, sess *Session, err error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)

	var userID string
	if err := tx.QueryRow(ctx, `INSERT INTO users DEFAULT VALUES RETURNING id::text`).Scan(&userID); err != nil {
		return "", nil, err
	}

	raw, sess, err = m.createSessionForTx(ctx, tx, userID, r)
	if err != nil {
		return "", nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}

	return raw, sess, nil
}

// CreateSessionFor issues a brand-new session for an existing user — used
// by passkey login, once a credential has been validated against a known
// user_id, to log that browser in without going through anonymous
// auto-provisioning.
func (m *Manager) CreateSessionFor(ctx context.Context, r *http.Request, userID string) (raw string, sess *Session, err error) {
	return m.createSessionForTx(ctx, m.pool, userID, r)
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx, so session creation
// can run standalone or as part of a larger transaction.
type execer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (m *Manager) createSessionForTx(ctx context.Context, db execer, userID string, r *http.Request) (raw string, sess *Session, err error) {
	raw, hash, err := generateToken()
	if err != nil {
		return "", nil, err
	}

	expiresAt := time.Now().AddDate(0, 0, m.cfg.AnonSessionTTLDays)
	ua := r.UserAgent()
	ip := ClientIP(r, m.cfg.TrustedProxy)

	var sessID string
	var lastSeen time.Time
	err = db.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, user_agent, created_ip)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, last_seen_at
	`, userID, hash, expiresAt, ua, ipOrNil(ip)).Scan(&sessID, &lastSeen)
	if err != nil {
		return "", nil, err
	}

	return raw, &Session{ID: sessID, UserID: userID, ExpireAt: expiresAt, LastSeen: lastSeen}, nil
}

// verify looks up a raw token, returning the session if valid and unexpired.
func (m *Manager) verify(ctx context.Context, raw string) (*Session, error) {
	hash := hashToken(raw)
	var s Session
	err := m.pool.QueryRow(ctx, `
		SELECT id::text, user_id::text, expires_at, last_seen_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > now()
	`, hash).Scan(&s.ID, &s.UserID, &s.ExpireAt, &s.LastSeen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	return &s, nil
}

// touch slides the session's expiry and last_seen_at, throttled so we're not
// writing on every single request.
func (m *Manager) touch(ctx context.Context, s *Session) {
	if time.Since(s.LastSeen) < time.Duration(m.cfg.SessionTouchMinIntervalHr)*time.Hour {
		return
	}
	newExpiry := time.Now().AddDate(0, 0, m.cfg.AnonSessionTTLDays)
	_, _ = m.pool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1
	`, s.ID, newExpiry)
	_, _ = m.pool.Exec(ctx, `UPDATE users SET last_seen_at = now() WHERE id = $1`, s.UserID)
}

// Logout deletes the current session server-side and clears the transport
// (cookie or tells the client to drop the header token).
func (m *Manager) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if raw, ok := m.readToken(r); ok {
		_, _ = m.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(raw))
	}
	if m.cfg.AnonIdentityTransport == "cookie" {
		http.SetCookie(w, &http.Cookie{
			Name:     m.cfg.AnonSessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   strings.HasPrefix(m.cfg.PublicBaseURL, "https://"),
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func (m *Manager) readToken(r *http.Request) (string, bool) {
	if m.cfg.AnonIdentityTransport == "header" {
		v := r.Header.Get(m.cfg.AnonSessionHeaderName)
		return v, v != ""
	}
	c, err := r.Cookie(m.cfg.AnonSessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

func (m *Manager) writeToken(w http.ResponseWriter, raw string) {
	if m.cfg.AnonIdentityTransport == "header" {
		// Header-transport clients receive the token in the JSON body of the
		// response instead (see httpapi), not as a response header.
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.AnonSessionCookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   m.cfg.AnonSessionTTLDays * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   strings.HasPrefix(m.cfg.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func ClientIP(r *http.Request, trustedProxy bool) string {
	if trustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func ipOrNil(ip string) any {
	if ip == "" {
		return nil
	}
	return ip
}

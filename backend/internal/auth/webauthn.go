package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	"share/backend/internal/config"
)

var ErrPasskeyDisabled = errors.New("auth: passkey accounts are disabled")

// webauthnUser adapts a DB-backed user + their stored credentials to the
// go-webauthn library's User interface. WebAuthnID is the user's UUID text
// as raw bytes — opaque and stable, which is all the spec requires.
type webauthnUser struct {
	id          string
	email       string
	credentials []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte                         { return []byte(u.id) }
func (u *webauthnUser) WebAuthnName() string                       { return displayName(u.email, u.id) }
func (u *webauthnUser) WebAuthnDisplayName() string                { return displayName(u.email, u.id) }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func displayName(email, id string) string {
	if email != "" {
		return email
	}
	return "Guest " + id[:8]
}

func newWebAuthn(cfg *config.Config) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPDisplayName: cfg.PasskeyRPName,
		RPID:          cfg.PasskeyRPID,
		RPOrigins:     []string{cfg.PasskeyOrigin},
	})
}

func (m *Manager) loadWebauthnUser(ctx context.Context, userID string) (*webauthnUser, error) {
	u := &webauthnUser{id: userID}
	var email *string
	if err := m.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return nil, err
	}
	if email != nil {
		u.email = *email
	}

	rows, err := m.pool.Query(ctx, `SELECT credential_json FROM webauthn_credentials WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var cred webauthn.Credential
		if err := json.Unmarshal(raw, &cred); err != nil {
			return nil, err
		}
		u.credentials = append(u.credentials, cred)
	}
	return u, rows.Err()
}

const ceremonyTTL = 2 * time.Minute

func (m *Manager) saveCeremony(ctx context.Context, kind string, userID *string, session *webauthn.SessionData) (string, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	var ceremonyID string
	err = m.pool.QueryRow(ctx, `
		INSERT INTO webauthn_ceremonies (kind, user_id, session_data, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, kind, userID, data, time.Now().Add(ceremonyTTL)).Scan(&ceremonyID)
	return ceremonyID, err
}

func (m *Manager) loadAndConsumeCeremony(ctx context.Context, ceremonyID, wantKind string) (*webauthn.SessionData, error) {
	var kind string
	var data []byte
	var expiresAt time.Time
	err := m.pool.QueryRow(ctx, `
		DELETE FROM webauthn_ceremonies WHERE id = $1
		RETURNING kind, session_data, expires_at
	`, ceremonyID).Scan(&kind, &data, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("auth: ceremony not found or already used")
		}
		return nil, err
	}
	if kind != wantKind {
		return nil, fmt.Errorf("auth: ceremony kind mismatch")
	}
	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("auth: ceremony expired")
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// BeginPasskeyRegistration starts an in-place upgrade: attaching a new
// passkey credential to the caller's current (possibly anonymous) user row.
func (m *Manager) BeginPasskeyRegistration(ctx context.Context, currentUserID string) (*protocol.CredentialCreation, string, error) {
	if !m.cfg.PasskeyAccountsEnabled {
		return nil, "", ErrPasskeyDisabled
	}
	user, err := m.loadWebauthnUser(ctx, currentUserID)
	if err != nil {
		return nil, "", err
	}
	creation, session, err := m.webauthn.BeginRegistration(user)
	if err != nil {
		return nil, "", err
	}
	ceremonyID, err := m.saveCeremony(ctx, "registration", &currentUserID, session)
	if err != nil {
		return nil, "", err
	}
	return creation, ceremonyID, nil
}

func (m *Manager) FinishPasskeyRegistration(ctx context.Context, r *http.Request, currentUserID, ceremonyID string) error {
	if !m.cfg.PasskeyAccountsEnabled {
		return ErrPasskeyDisabled
	}
	session, err := m.loadAndConsumeCeremony(ctx, ceremonyID, "registration")
	if err != nil {
		return err
	}
	user, err := m.loadWebauthnUser(ctx, currentUserID)
	if err != nil {
		return err
	}
	cred, err := m.webauthn.FinishRegistration(user, *session, r)
	if err != nil {
		return err
	}
	credJSON, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = m.pool.Exec(ctx, `
		INSERT INTO webauthn_credentials (user_id, credential_id, credential_json)
		VALUES ($1, $2, $3)
	`, currentUserID, cred.ID, credJSON)
	return err
}

// BeginPasskeyLogin starts a discoverable (usernameless) login ceremony.
func (m *Manager) BeginPasskeyLogin(ctx context.Context) (*protocol.CredentialAssertion, string, error) {
	if !m.cfg.PasskeyAccountsEnabled {
		return nil, "", ErrPasskeyDisabled
	}
	assertion, session, err := m.webauthn.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", err
	}
	ceremonyID, err := m.saveCeremony(ctx, "login", nil, session)
	if err != nil {
		return nil, "", err
	}
	return assertion, ceremonyID, nil
}

// FinishPasskeyLogin validates the assertion, resolves which user it
// belongs to, bumps that credential's stored sign count (clone-detection
// bookkeeping), and issues + writes (cookie or header, per config) a fresh
// session for that user onto the response.
func (m *Manager) FinishPasskeyLogin(ctx context.Context, w http.ResponseWriter, r *http.Request, ceremonyID string) error {
	if !m.cfg.PasskeyAccountsEnabled {
		return ErrPasskeyDisabled
	}
	session, err := m.loadAndConsumeCeremony(ctx, ceremonyID, "login")
	if err != nil {
		return err
	}

	var resolvedUserID string
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		resolvedUserID = string(userHandle)
		return m.loadWebauthnUser(ctx, resolvedUserID)
	}

	_, cred, err := m.webauthn.FinishPasskeyLogin(handler, *session, r)
	if err != nil {
		return err
	}

	credJSON, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if _, err := m.pool.Exec(ctx, `
		UPDATE webauthn_credentials SET credential_json = $2 WHERE credential_id = $1
	`, cred.ID, credJSON); err != nil {
		return err
	}

	raw, _, err := m.CreateSessionFor(ctx, r, resolvedUserID)
	if err != nil {
		return err
	}
	m.writeToken(w, raw)
	if m.cfg.AnonIdentityTransport == "header" {
		w.Header().Set(m.cfg.AnonSessionHeaderName, raw)
	}
	return nil
}

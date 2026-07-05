package auth

import (
	"context"
	"encoding/json"
	"net/http"
)

type contextKey int

const userIDKey contextKey = iota

// UserID returns the identified caller's user id, set by Identify.
func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok
}

// Identify resolves the caller's identity for every request: an existing
// valid session (anonymous or not) is reused as-is; otherwise, if anonymous
// accounts are enabled, a brand-new anonymous user+session is silently
// provisioned. No route needs a separate "logged in" check beyond this.
func (m *Manager) Identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if raw, ok := m.readToken(r); ok {
			sess, err := m.verify(ctx, raw)
			if err == nil {
				m.touch(ctx, sess)
				next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userIDKey, sess.UserID)))
				return
			}
		}

		if !m.cfg.AnonAccountsEnabled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "login_required"})
			return
		}

		raw, sess, err := m.createAnonymousSession(ctx, r)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		m.writeToken(w, raw)
		if m.cfg.AnonIdentityTransport == "header" {
			w.Header().Set(m.cfg.AnonSessionHeaderName, raw)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userIDKey, sess.UserID)))
	})
}

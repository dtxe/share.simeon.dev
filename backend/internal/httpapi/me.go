package httpapi

import (
	"encoding/json"
	"net/http"

	"share/backend/internal/auth"
)

type meResponse struct {
	Email           *string `json:"email"`
	HasEmail        bool    `json:"hasEmail"`
	HasPasskey      bool    `json:"hasPasskey"`
	PasskeysEnabled bool    `json:"passkeysEnabled"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var email *string
	if err := s.Pool.QueryRow(r.Context(), `
		SELECT email FROM users WHERE id = $1
	`, userID).Scan(&email); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var hasPasskey bool
	if err := s.Pool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM webauthn_credentials WHERE user_id = $1)
	`, userID).Scan(&hasPasskey); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meResponse{
		Email:           email,
		HasEmail:        email != nil,
		HasPasskey:      hasPasskey,
		PasskeysEnabled: s.Cfg.PasskeyAccountsEnabled,
	})
}

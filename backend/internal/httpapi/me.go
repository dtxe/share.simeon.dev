package httpapi

import (
	"encoding/json"
	"net/http"

	"cher-app/backend/internal/auth"
)

type meResponse struct {
	HasEmail   bool `json:"hasEmail"`
	HasPasskey bool `json:"hasPasskey"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var hasEmail bool
	if err := s.Pool.QueryRow(r.Context(), `
		SELECT email IS NOT NULL FROM users WHERE id = $1
	`, userID).Scan(&hasEmail); err != nil {
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
	_ = json.NewEncoder(w).Encode(meResponse{HasEmail: hasEmail, HasPasskey: hasPasskey})
}

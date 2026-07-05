package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"share/backend/internal/auth"
)

func (s *Server) handlePasskeyRegisterOptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	creation, ceremonyID, err := s.Auth.BeginPasskeyRegistration(r.Context(), userID)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ceremonyId": ceremonyID, "options": creation})
}

func (s *Server) handlePasskeyRegisterVerify(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ceremonyID := r.URL.Query().Get("ceremonyId")
	if ceremonyID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing ceremonyId")
		return
	}
	if err := s.Auth.FinishPasskeyRegistration(r.Context(), r, userID, ceremonyID); err != nil {
		writePasskeyError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePasskeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	assertion, ceremonyID, err := s.Auth.BeginPasskeyLogin(r.Context())
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ceremonyId": ceremonyID, "options": assertion})
}

func (s *Server) handlePasskeyLoginVerify(w http.ResponseWriter, r *http.Request) {
	ceremonyID := r.URL.Query().Get("ceremonyId")
	if ceremonyID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing ceremonyId")
		return
	}
	if err := s.Auth.FinishPasskeyLogin(r.Context(), w, r, ceremonyID); err != nil {
		writePasskeyError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writePasskeyError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrPasskeyDisabled) {
		writeJSONError(w, http.StatusNotFound, "passkey accounts are not enabled")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "passkey ceremony failed")
}

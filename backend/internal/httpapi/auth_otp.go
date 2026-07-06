package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"share/backend/internal/auth"
)

const (
	maxOTPBodyBytes = 1 << 10 // 1KiB — an email + 6-digit code never needs more
	maxEmailLen     = 254     // RFC 5321 max mailbox length
	maxOTPCodeLen   = 16
)

type otpRequestBody struct {
	Email string `json:"email"`
}

func (s *Server) handleOTPRequest(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.EmailOTPEnabled {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxOTPBodyBytes)
	var body otpRequestBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Email) > maxEmailLen {
		writeJSONError(w, http.StatusBadRequest, "invalid email")
		return
	}

	addr, err := mail.ParseAddress(strings.TrimSpace(body.Email))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid email")
		return
	}

	err = s.Auth.RequestOTP(r.Context(), s.Email, addr.Address)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, auth.ErrOTPCooldown):
		writeJSONError(w, http.StatusTooManyRequests, "please wait before requesting another code")
	default:
		writeJSONError(w, http.StatusInternalServerError, "could not send code")
	}
}

type otpVerifyBody struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (s *Server) handleOTPVerify(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.EmailOTPEnabled {
		http.NotFound(w, r)
		return
	}

	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxOTPBodyBytes)
	var body otpVerifyBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Email) > maxEmailLen || len(body.Code) > maxOTPCodeLen {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	addr, err := mail.ParseAddress(strings.TrimSpace(body.Email))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid email")
		return
	}

	_, err = s.Auth.VerifyOTP(r.Context(), w, r, userID, addr.Address, strings.TrimSpace(body.Code))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, auth.ErrOTPInvalid):
		writeJSONError(w, http.StatusBadRequest, "invalid or expired code")
	case errors.Is(err, auth.ErrOTPTooManyTries):
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, request a new code")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(r.Context(), w, r)
	w.WriteHeader(http.StatusOK)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

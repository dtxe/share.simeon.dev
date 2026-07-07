package httpapi

import (
	"log"
	"net/http"

	"share/backend/internal/auth"
)

// On a Redis error, these fail open (log and let the request through)
// rather than taking the whole API down over a Redis hiccup — the
// Postgres-side per-session extract_count cap is the backstop for the one
// route (extraction) where failing open would actually matter for cost.

func (s *Server) globalRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := auth.ClientIP(r, s.Cfg.TrustedProxy, s.Cfg.RealIPHeader)
		ok, err := s.RL.AllowGlobalPerIP(r.Context(), ip)
		if err != nil {
			log.Printf("ratelimit: global check failed, failing open: %v", err)
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			writeJSONError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimitOTPRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := auth.ClientIP(r, s.Cfg.TrustedProxy, s.Cfg.RealIPHeader)
		ok, err := s.RL.AllowOTPRequestPerIP(r.Context(), ip, s.Cfg.OTPRequestRatePerIPPerHr)
		if err != nil {
			log.Printf("ratelimit: otp check failed, failing open: %v", err)
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			writeJSONError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimitOTPVerify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := auth.ClientIP(r, s.Cfg.TrustedProxy, s.Cfg.RealIPHeader)
		ok, err := s.RL.AllowOTPVerifyPerIP(r.Context(), ip, s.Cfg.OTPVerifyRatePerIPPerHr)
		if err != nil {
			log.Printf("ratelimit: otp verify check failed, failing open: %v", err)
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			writeJSONError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

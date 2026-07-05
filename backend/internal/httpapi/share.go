package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"cher-app/backend/internal/auth"
)

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")

	token, err := s.Store.GenerateShareToken(r.Context(), sessionID, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"viewToken": token,
		"shareUrl":  s.Cfg.PublicBaseURL + "/s/" + token,
	})
}

// PublicRouter is mounted separately from the main /api group in
// NewRouter — deliberately outside the Identify middleware, so a
// public/view-token request never has session/auth machinery in its
// call path and can never reach a mutating handler.
func (s *Server) handlePublicView(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	ip := auth.ClientIP(r, s.Cfg.TrustedProxy)

	sess, result, err := s.Store.GetBreakdownByViewToken(r.Context(), token)
	if err != nil {
		if allowed, rlErr := s.RL.AllowInvalidViewTokenPerIP(r.Context(), ip); rlErr == nil && !allowed {
			writeJSONError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	// Deliberately built by hand (not sessionSummary()) so the internal
	// session UUID and any future field added to the owner-facing summary
	// don't leak here just because someone forgot to update this endpoint.
	writeJSON(w, http.StatusOK, map[string]any{
		"title":          sess.Title,
		"restaurantName": sess.RestaurantName,
		"billDate":       sess.BillDate,
		"subtotalCents":  sess.SubtotalCents,
		"totalPaidCents": sess.TotalPaidCents,
		"hasReceipt":     sess.ReceiptImagePath != nil,
		"result":         result,
	})
}

func (s *Server) handlePublicReceipt(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	sess, err := s.Store.GetByViewToken(r.Context(), token)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveReceipt(w, sess)
}

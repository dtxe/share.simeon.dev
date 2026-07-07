package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"share/backend/internal/auth"
	"share/backend/internal/llm"
	"share/backend/internal/receipts"
	"share/backend/internal/store"
)

const maxExtractPerSession = 5

func (s *Server) handleUploadReceipt(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")

	if _, err := s.Store.GetSession(r.Context(), sessionID, userID); err != nil {
		storeErrToStatus(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, receipts.MaxUploadBytes)
	if err := r.ParseMultipartForm(receipts.MaxUploadBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "upload too large or malformed")
		return
	}
	file, _, err := r.FormFile("receipt")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing 'receipt' file field")
		return
	}
	defer file.Close()

	relPath, err := s.Receipts.Save(sessionID, file)
	if err != nil {
		if s.Cfg.Debug {
			log.Printf("debug: receipt upload session=%s: %v", sessionID, err)
		}
		writeJSONError(w, http.StatusBadRequest, "could not process image")
		return
	}

	if err := s.Store.SetReceiptImagePath(r.Context(), sessionID, userID, relPath); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetReceipt(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sess, err := s.Store.GetSession(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	s.serveReceipt(w, sess)
}

func (s *Server) serveReceipt(w http.ResponseWriter, sess *store.BillSession) {
	if sess.ReceiptImagePath == nil {
		writeJSONError(w, http.StatusNotFound, "no receipt uploaded")
		return
	}
	f, err := s.Receipts.Open(*sess.ReceiptImagePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "receipt not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", `inline; filename="receipt.jpg"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, f)
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")
	ctx := r.Context()

	sess, err := s.Store.GetSession(ctx, sessionID, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	if sess.ReceiptImagePath == nil {
		writeJSONError(w, http.StatusBadRequest, "upload a receipt first")
		return
	}

	ip := auth.ClientIP(r, s.Cfg.TrustedProxy)
	if allowed, err := s.RL.AllowExtractPerIP(ctx, ip); err == nil && !allowed {
		writeJSONError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	underCap, err := s.Store.IncrementExtractCount(ctx, sessionID, userID, maxExtractPerSession)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !underCap {
		writeJSONError(w, http.StatusTooManyRequests, "extraction limit reached for this bill")
		return
	}

	const estimatedCents = 1 // conservative per-call placeholder; corrected below via AdjustLLMSpend once real usage is known
	reserved, err := s.RL.ReserveLLMSpend(ctx, estimatedCents, s.Cfg.LLMDailySpendCapCents)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !reserved {
		writeJSONError(w, http.StatusServiceUnavailable, "daily extraction budget reached, try manual entry")
		return
	}

	f, err := s.Receipts.Open(*sess.ReceiptImagePath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()

	imageBytes, err := io.ReadAll(f)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	extractCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, extractErr := s.LLM.ExtractReceipt(extractCtx, imageBytes, "image/jpeg")

	status := "success"
	var errMsg *string
	var rawResponse []byte
	if extractErr != nil {
		status = "error"
		msg := "extraction failed"
		errMsg = &msg
		if s.Cfg.Debug {
			log.Printf("debug: extract session=%s provider=%s: %v", sessionID, s.LLM.Name(), extractErr)
		}
	} else {
		actualCents := estimateCostCents(result.Usage, s.Cfg.LLMCostPer1KTokensCents)
		_ = s.RL.AdjustLLMSpend(ctx, actualCents-estimatedCents)
		rawResponse, _ = json.Marshal(result.Receipt)
	}

	_, _ = s.Pool.Exec(ctx, `
		INSERT INTO extraction_runs (session_id, provider, model, status, error_message, raw_response)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, sessionID, s.LLM.Name(), s.Cfg.LLMModel, status, errMsg, rawResponse)

	if extractErr != nil {
		// Don't leak upstream error detail to the client — internal/llm's own
		// error wrapping avoids echoing request content, but stay generic here too.
		writeJSONError(w, http.StatusBadGateway, "extraction failed")
		return
	}

	// Persist server-side rather than waiting on the client to round-trip a
	// separate dishes/bulk save — the client only gets a reviewed *edit* of
	// what's already saved, not the sole path to saving it at all.
	newDishes := make([]store.NewDish, 0, len(result.Receipt.Items))
	for _, it := range result.Receipt.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		newDishes = append(newDishes, store.NewDish{
			Name:           name,
			UnitPriceCents: it.PriceCents,
			Quantity:       it.Quantity,
			Source:         "llm_extracted",
		})
	}
	if _, err := s.Store.ReplaceDishes(ctx, sessionID, userID, newDishes); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "extraction succeeded but saving items failed")
		return
	}

	// Best-effort metadata fill: never clobber a value the user already
	// entered (title is left alone entirely — the client suggests one from
	// restaurantName/date on the Settle screen instead of us writing it here).
	patch := store.SessionPatch{}
	hasPatch := false
	if name := strings.TrimSpace(result.Receipt.RestaurantName); name != "" {
		if len(name) > 120 {
			name = name[:120]
		}
		patch.RestaurantName = &name
		hasPatch = true
	}
	if result.Receipt.Date != "" {
		if parsed, err := time.Parse("2006-01-02", result.Receipt.Date); err == nil {
			patch.BillDate = &parsed
			hasPatch = true
		}
	}
	if sess.TotalPaidCents == nil {
		total := result.Receipt.TotalPaidCents
		if total <= 0 && result.Receipt.SubtotalCents > 0 && result.Receipt.TipCents > 0 {
			total = result.Receipt.SubtotalCents + result.Receipt.TipCents
		}
		if total > 0 && total <= 5_000_000_00 {
			patch.TotalPaidCents = &total
			hasPatch = true
		}
	}
	if hasPatch {
		if err := s.Store.UpdateSession(ctx, sessionID, userID, patch); err != nil && s.Cfg.Debug {
			log.Printf("debug: extract metadata patch session=%s: %v", sessionID, err)
		}
	}

	writeJSON(w, http.StatusOK, result.Receipt)
}

func estimateCostCents(usage llm.Usage, costPer1KTokensCents float64) int {
	total := usage.PromptTokens + usage.CompletionTokens
	// Round rather than truncate — floor-ing every call systematically
	// underreports spend against the daily cap, letting real spend drift
	// past it over many calls.
	return int(math.Round(float64(total) / 1000.0 * costPer1KTokensCents))
}

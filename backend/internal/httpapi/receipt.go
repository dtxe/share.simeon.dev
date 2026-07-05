package httpapi

import (
	"context"
	"io"
	"net/http"
	"os"
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
	if extractErr != nil {
		status = "error"
		msg := "extraction failed"
		errMsg = &msg
	} else {
		actualCents := estimateCostCents(result.Usage, s.Cfg.LLMCostPer1KTokensCents)
		_ = s.RL.AdjustLLMSpend(ctx, actualCents-estimatedCents)
	}

	_, _ = s.Pool.Exec(ctx, `
		INSERT INTO extraction_runs (session_id, provider, model, status, error_message)
		VALUES ($1, $2, $3, $4, $5)
	`, sessionID, s.LLM.Name(), s.Cfg.LLMModel, status, errMsg)

	if extractErr != nil {
		// Don't leak upstream error detail to the client — internal/llm's own
		// error wrapping avoids echoing request content, but stay generic here too.
		writeJSONError(w, http.StatusBadGateway, "extraction failed")
		return
	}

	writeJSON(w, http.StatusOK, result.Receipt)
}

func estimateCostCents(usage llm.Usage, costPer1KTokensCents float64) int {
	total := usage.PromptTokens + usage.CompletionTokens
	return int(float64(total) / 1000.0 * costPer1KTokensCents)
}

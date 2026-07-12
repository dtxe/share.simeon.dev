package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"share/backend/internal/auth"
	"share/backend/internal/extraction"
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

	ip := auth.ClientIP(r, s.Cfg.TrustedProxy, s.Cfg.RealIPHeader)
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

	maxCalls := s.Extractor.MaxCalls()
	if maxCalls <= 0 {
		log.Printf("extract: strategy %q returned invalid MaxCalls %d", s.Extractor.Name(), maxCalls)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if maxCalls > s.Cfg.LLMMaxSpendPerReceiptCents/extraction.ReservationCentsPerCall {
		writeJSONError(w, http.StatusServiceUnavailable, "extraction strategy exceeds the per-receipt budget")
		return
	}
	reservedCents := extraction.ReservationCentsPerCall * maxCalls
	runID, err := s.Store.BeginExtractionRun(ctx, store.BeginExtractionRunInput{
		SessionID:       sessionID,
		Strategy:        s.Extractor.Name(),
		MaxCalls:        maxCalls,
		ReceiptCapCents: s.Cfg.LLMMaxSpendPerReceiptCents,
		ReservedCents:   reservedCents,
	})
	if err != nil {
		log.Printf("extract: begin telemetry session=%s: %v", sessionID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	f, err := s.Receipts.Open(*sess.ReceiptImagePath)
	if err != nil {
		s.completeEmptyExtractionRun(runID, "error", "opening receipt failed")
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()

	imageBytes, err := io.ReadAll(f)
	if err != nil {
		s.completeEmptyExtractionRun(runID, "error", "reading receipt failed")
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	reservation, reserved, err := s.RL.ReserveLLMSpendDetailed(ctx, reservedCents, s.Cfg.LLMDailySpendCapCents)
	if err != nil {
		s.completeEmptyExtractionRun(runID, "error", "spend reservation failed")
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !reserved {
		s.completeEmptyExtractionRun(runID, "rejected", "daily extraction budget reached")
		writeJSONError(w, http.StatusServiceUnavailable, "daily extraction budget reached, try manual entry")
		return
	}

	extractCtx, cancel := context.WithTimeout(ctx, 60*time.Second*time.Duration(maxCalls))
	defer cancel()

	runResult, runErr := s.Extractor.Run(extractCtx, imageBytes, "image/jpeg")
	if runErr == nil && len(runResult.Attempts) == 0 {
		runErr = fmt.Errorf("strategy succeeded without reporting an attempt")
	}
	attemptRows, accountedCents, knownActualCost, contractErr := buildAttemptTelemetry(runResult.Attempts, maxCalls)
	if contractErr != nil {
		runErr = errors.Join(runErr, contractErr)
	}
	accountingCtx, accountingCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_, reconcileErr := s.RL.FinalizeLLMSpend(accountingCtx, reservation, accountedCents)
	if reconcileErr != nil {
		log.Printf("extract: reconcile spend run=%s: %v", runID, reconcileErr)
	}
	terminalStatus := "success"
	var runErrMsg *string
	if runErr != nil {
		terminalStatus = "error"
		msg := "extraction failed"
		runErrMsg = &msg
	}
	accountedCost := accountedCents
	telemetryErr := s.Store.CompleteExtractionRun(accountingCtx, store.CompleteExtractionRunInput{
		RunID:                runID,
		Status:               terminalStatus,
		ErrorMessage:         runErrMsg,
		SubtotalMatched:      runResult.SubtotalMatched,
		SubtotalDiffCents:    runResult.SubtotalDiffCents,
		KnownActualCostCents: knownActualCost,
		AccountedCostCents:   &accountedCost,
		ReservationAccepted:  true,
		SpendReconciled:      reconcileErr == nil,
		CompletedAt:          time.Now(),
		Attempts:             attemptRows,
	})
	accountingCancel()
	if telemetryErr != nil {
		log.Printf("extract: complete telemetry run=%s: %v", runID, telemetryErr)
		writeJSONError(w, http.StatusInternalServerError, "extraction telemetry failed")
		return
	}
	if runErr != nil {
		if s.Cfg.Debug {
			log.Printf("debug: extract session=%s strategy=%s: %v", sessionID, s.Extractor.Name(), runErr)
		}
		writeJSONError(w, http.StatusBadGateway, "extraction failed")
		return
	}

	// Persist server-side rather than waiting on the client to round-trip a
	// separate dishes/bulk save — the client only gets a reviewed *edit* of
	// what's already saved, not the sole path to saving it at all.
	newDishes := make([]store.NewDish, 0, len(runResult.Receipt.Items))
	for _, it := range runResult.Receipt.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		qty := math.Round(it.Quantity*100) / 100
		if qty <= 0 {
			qty = 1
		}
		if qty != 1 {
			name = formatQtyPrefix(qty) + name
		}
		lineTotal := int64(math.Round(float64(it.PriceCents) * qty))
		newDishes = append(newDishes, store.NewDish{
			Name:           name,
			UnitPriceCents: lineTotal,
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
	if name := strings.TrimSpace(runResult.Receipt.RestaurantName); name != "" {
		if len(name) > 120 {
			name = name[:120]
		}
		patch.RestaurantName = &name
		hasPatch = true
	}
	if runResult.Receipt.Date != "" {
		if parsed, err := time.Parse("2006-01-02", runResult.Receipt.Date); err == nil {
			patch.BillDate = &parsed
			hasPatch = true
		}
	}
	if sess.TotalPaidCents == nil {
		total := runResult.Receipt.TotalPaidCents
		if total <= 0 && runResult.Receipt.SubtotalCents > 0 && runResult.Receipt.TipCents > 0 {
			total = runResult.Receipt.SubtotalCents + runResult.Receipt.TipCents
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

	// After a successful extraction, the LLM has already seen the full-quality
	// original. Compress the stored file asynchronously so storage and share
	// links serve the smaller version, replacing the original on disk.
	if !sess.ReceiptImageCompressed && sess.ReceiptImagePath != nil {
		go func(path string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			_, _, err := s.Receipts.Compress(path)
			if err != nil {
				if s.Cfg.Debug {
					log.Printf("debug: compress receipt session=%s path=%s: %v", sessionID, path, err)
				}
				return
			}
			if err := s.Store.MarkReceiptImageCompressed(bgCtx, sessionID, userID); err != nil && s.Cfg.Debug {
				log.Printf("debug: mark receipt compressed session=%s: %v", sessionID, err)
			}
		}(*sess.ReceiptImagePath)
	}

	writeJSON(w, http.StatusOK, runResult.Receipt)
}

// formatQtyPrefix formats a quantity as a compact name prefix, e.g. "2x " or "0.5x ".
func formatQtyPrefix(qty float64) string {
	return strconv.FormatFloat(qty, 'f', -1, 64) + "x "
}

func (s *Server) completeEmptyExtractionRun(runID, status, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	zero := 0
	if err := s.Store.CompleteExtractionRun(ctx, store.CompleteExtractionRunInput{
		RunID:                runID,
		Status:               status,
		ErrorMessage:         &message,
		KnownActualCostCents: &zero,
		AccountedCostCents:   &zero,
		SpendReconciled:      true,
		CompletedAt:          time.Now(),
	}); err != nil {
		log.Printf("extract: complete empty telemetry run=%s: %v", runID, err)
	}
}

func buildAttemptTelemetry(attempts []extraction.Attempt, maxCalls int) ([]store.ExtractionAttempt, int, *int, error) {
	var contractErr error
	if len(attempts) > maxCalls {
		contractErr = fmt.Errorf("strategy reported %d attempts, MaxCalls is %d", len(attempts), maxCalls)
	}

	rows := make([]store.ExtractionAttempt, 0, len(attempts))
	accountedCents := 0
	knownActualCents := 0
	allCostsKnown := true
	maxInt := int(^uint(0) >> 1)

	for i, attempt := range attempts {
		if contractErr == nil && (attempt.Provider == "" || attempt.Model == "") {
			contractErr = fmt.Errorf("attempt %d is missing provider or model", i+1)
		}
		status := "success"
		var errMsg *string
		if attempt.Err != nil {
			status = "error"
			msg := attempt.Err.Error()
			errMsg = &msg
		}
		promptTok, completeTok := attempt.PromptTok, attempt.CompleteTok
		costCents := attempt.CostCents
		if costCents == nil || *costCents < 0 || accountedCents > maxInt-*costCents {
			if costCents != nil && *costCents < 0 && contractErr == nil {
				contractErr = fmt.Errorf("attempt %d reported a negative cost", i+1)
			}
			costCents = nil
			allCostsKnown = false
			accountedCents += extraction.ReservationCentsPerCall
		} else {
			knownActualCents += *costCents
			accountedCents += *costCents
		}
		rows = append(rows, store.ExtractionAttempt{
			Attempt:           i + 1,
			Provider:          attempt.Provider,
			Model:             attempt.Model,
			Status:            status,
			ErrorMessage:      errMsg,
			RawResponse:       attempt.RawJSON,
			PromptTokens:      &promptTok,
			CompletionTokens:  &completeTok,
			CostCents:         costCents,
			SubtotalMatched:   attempt.SubtotalMatched,
			SubtotalDiffCents: attempt.SubtotalDiffCents,
		})
	}

	if allCostsKnown {
		return rows, accountedCents, &knownActualCents, contractErr
	}
	return rows, accountedCents, nil, contractErr
}

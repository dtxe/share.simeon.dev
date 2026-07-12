package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"share/backend/internal/auth"
	"share/backend/internal/cleanup"
	"share/backend/internal/config"
	"share/backend/internal/db"
	"share/backend/internal/email"
	"share/backend/internal/extraction"
	"share/backend/internal/extraction/baseline"
	"share/backend/internal/extraction/deterministic"
	"share/backend/internal/extraction/feedback"
	"share/backend/internal/httpapi"
	"share/backend/internal/llm"
	"share/backend/internal/llm/fireworks"
	"share/backend/internal/llm/openai"
	"share/backend/internal/llm/openaicompat"
	"share/backend/internal/ratelimit"
	"share/backend/internal/receipts"
	"share/backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	authManager, err := auth.NewManager(pool, cfg)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	limiter, err := ratelimit.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("ratelimit: %v", err)
	}
	if err := limiter.Ping(ctx); err != nil {
		log.Fatalf("ratelimit: redis ping: %v", err)
	}

	var emailProvider email.Provider
	switch cfg.EmailProvider {
	case "smtp":
		emailProvider = &email.SMTPProvider{
			Host:    cfg.SMTPHost,
			Port:    cfg.SMTPPort,
			User:    cfg.SMTPUser,
			Pass:    cfg.SMTPPass,
			From:    cfg.SMTPFrom,
			TLSMode: cfg.SMTPTLSMode,
		}
	default:
		log.Fatalf("unknown EMAIL_PROVIDER %q", cfg.EmailProvider)
	}

	var llmProvider llm.Provider
	switch cfg.LLMProvider {
	case "fireworks":
		llmProvider = fireworks.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	case "openai":
		llmProvider = openai.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	default:
		log.Fatalf("unknown LLM_PROVIDER %q", cfg.LLMProvider)
	}

	var extractor extraction.Strategy
	switch cfg.ExtractionStrategy {
	case "baseline":
		extractor = baseline.New(llmProvider, cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "deterministic_check":
		llmClient := openaicompat.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		extractor = deterministic.New(llmClient, llmProvider.Name(), cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "feedback_retry":
		llmClient := openaicompat.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		extractor = feedback.New(llmClient, llmProvider.Name(), cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "ocr_first":
		log.Fatalf("EXTRACTION_STRATEGY=ocr_first is experimental and only supported by extractbench (see cmd/extractbench)")
	default:
		log.Fatalf("unknown EXTRACTION_STRATEGY %q", cfg.ExtractionStrategy)
	}
	if extractor.MaxCalls() <= 0 {
		log.Fatalf("extraction strategy %q has invalid MaxCalls %d", extractor.Name(), extractor.MaxCalls())
	}
	if extractor.MaxCalls() > cfg.LLMMaxSpendPerReceiptCents/extraction.ReservationCentsPerCall {
		log.Fatalf("extraction strategy %q requires %d cents but per-receipt cap is %d cents",
			extractor.Name(), extractor.MaxCalls()*extraction.ReservationCentsPerCall, cfg.LLMMaxSpendPerReceiptCents)
	}
	st := store.New(pool)
	rs := receipts.New(cfg.UploadDir)

	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	defer stopCleanup()
	go cleanup.Run(cleanupCtx, st, rs)

	router := httpapi.NewRouter(&httpapi.Server{
		Pool:      pool,
		Cfg:       cfg,
		Auth:      authManager,
		Email:     emailProvider,
		RL:        limiter,
		Extractor: extractor,
		Store:     st,
		Receipts:  rs,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second, // covers the 60s LLM extraction call

		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

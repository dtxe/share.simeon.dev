package main

import (
	"context"
	"log"
	"net/http"

	"cher-app/backend/internal/auth"
	"cher-app/backend/internal/cleanup"
	"cher-app/backend/internal/config"
	"cher-app/backend/internal/db"
	"cher-app/backend/internal/email"
	"cher-app/backend/internal/httpapi"
	"cher-app/backend/internal/llm"
	"cher-app/backend/internal/llm/fireworks"
	"cher-app/backend/internal/llm/openai"
	"cher-app/backend/internal/ratelimit"
	"cher-app/backend/internal/receipts"
	"cher-app/backend/internal/store"
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
			Host: cfg.SMTPHost,
			Port: cfg.SMTPPort,
			User: cfg.SMTPUser,
			Pass: cfg.SMTPPass,
			From: cfg.SMTPFrom,
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

	st := store.New(pool)
	rs := receipts.New(cfg.UploadDir)

	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	defer stopCleanup()
	go cleanup.Run(cleanupCtx, st, rs)

	router := httpapi.NewRouter(&httpapi.Server{
		Pool:     pool,
		Cfg:      cfg,
		Auth:     authManager,
		Email:    emailProvider,
		RL:       limiter,
		LLM:      llmProvider,
		Store:    st,
		Receipts: rs,
	})

	log.Printf("listening on %s", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
		log.Fatal(err)
	}
}

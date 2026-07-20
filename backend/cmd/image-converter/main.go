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

	"share/backend/internal/imageconverter"
)

func main() {
	exe := os.Getenv("MAGICK_EXECUTABLE")
	if exe == "" {
		exe = "magick"
	}
	service := imageconverter.New(imageconverter.ExecRunner{Exe: exe}, 4)
	preflightCtx, cancelPreflight := context.WithTimeout(context.Background(), 5*time.Second)
	if err := service.Preflight(preflightCtx); err != nil {
		cancelPreflight()
		log.Fatalf("ImageMagick preflight: %v", err)
	}
	cancelPreflight()
	server := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: service.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		log.Printf("image converter listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

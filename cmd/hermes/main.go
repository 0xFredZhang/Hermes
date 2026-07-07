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

	"github.com/0xFredZhang/Hermes/internal/api"
	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/config"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Derive separate keys for AES-GCM and session HMAC
	aesKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:aes-gcm:v1")
	if err != nil {
		log.Fatalf("derive aes key: %v", err)
	}
	sessionKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:session-hmac:v1")
	if err != nil {
		log.Fatalf("derive session key: %v", err)
	}

	cipher, err := crypto.NewCipher(aesKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	st, err := store.Open(cfg.DBPath, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	renderer, err := web.NewRenderer()
	if err != nil {
		log.Fatalf("renderer: %v", err)
	}

	deps := api.Deps{
		Store:     st,
		Validator: cloud.NewValidator(),
		Auth:      auth.New(cfg.LoginPassword, sessionKey),
		Renderer:  renderer,
	}

	// Setup HTTP server with timeouts
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Setup graceful shutdown on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("hermes listening on %s", cfg.Addr)

	// Run server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Print("shutting down...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	st.Close()
}

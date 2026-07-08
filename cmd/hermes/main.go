package main

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/0xFredZhang/Hermes/internal/api"
	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/config"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/provisioner/pulumiengine"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	aesKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:aes-gcm:v1")
	if err != nil {
		log.Fatalf("derive aes key: %v", err)
	}
	sessionKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:session-hmac:v1")
	if err != nil {
		log.Fatalf("derive session key: %v", err)
	}
	ppKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:pulumi-passphrase:v1")
	if err != nil {
		log.Fatalf("derive pulumi passphrase: %v", err)
	}
	passphrase := base64.StdEncoding.EncodeToString(ppKey)

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

	// Ensure the local Pulumi state directory exists for the file:// backend.
	if dir, ok := strings.CutPrefix(cfg.PulumiBackend, "file://"); ok {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("pulumi state dir: %v", err)
		}
	}

	broker := orchestrator.NewBroker()
	prov := pulumiengine.New(cfg.PulumiProject, cfg.PulumiBackend, passphrase)
	orch := orchestrator.New(st, prov, broker, cfg.Workers)
	orch.Start(context.Background())

	deps := api.Deps{
		Store:        st,
		Validator:    cloud.NewValidator(),
		Auth:         auth.New(cfg.LoginPassword, sessionKey),
		Renderer:     renderer,
		Orchestrator: orch,
		Broker:       broker,
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// No global WriteTimeout: SSE streams are long-lived. Per-request
		// deadlines are managed where needed (login/forms are quick anyway).
		IdleTimeout: 60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("hermes listening on %s (workers=%d, backend=%s)", cfg.Addr, cfg.Workers, cfg.PulumiBackend)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	// Cancel workers: an in-flight provisioning job's context is cancelled, so it
	// aborts and is reconciled to failed on the next startup (crash recovery).
	// The DB is closed by the deferred st.Close() as main returns.
	orch.Stop()
}

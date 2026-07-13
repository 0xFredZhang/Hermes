package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/provisioner/pulumiengine"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	// A transitive dependency (k8s.io/component-base/logs, pulled in via the AWS
	// Pulumi provider) hijacks the standard logger in its init(): it redirects
	// output to klog's writer and zeroes the flags, which silently swallows our
	// log lines. Restore stderr + timestamps before we log anything.
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], defaultCommandDependencies()); err != nil {
		log.Fatalf("hermes: %v", err)
	}
}

func runServer(ctx context.Context) (runErr error) {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	aesKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:aes-gcm:v1")
	if err != nil {
		return fmt.Errorf("derive aes key: %w", err)
	}
	sessionKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:session-hmac:v1")
	if err != nil {
		return fmt.Errorf("derive session key: %w", err)
	}
	ppKey, err := crypto.DeriveKey(cfg.MasterKey, "hermes:pulumi-passphrase:v1")
	if err != nil {
		return fmt.Errorf("derive pulumi passphrase: %w", err)
	}
	passphrase := base64.StdEncoding.EncodeToString(ppKey)

	cipher, err := crypto.NewCipher(aesKey)
	if err != nil {
		return fmt.Errorf("cipher: %w", err)
	}
	st, err := store.Open(cfg.DBPath, cipher)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close store: %w", err))
		}
	}()
	renderer, err := web.NewRenderer()
	if err != nil {
		return fmt.Errorf("renderer: %w", err)
	}

	// Ensure the validated local Pulumi state directory exists.
	if dir, isFile, err := config.LocalPulumiBackendPath(cfg.PulumiBackend); err != nil {
		return fmt.Errorf("pulumi state backend: %w", err)
	} else if isFile {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("pulumi state dir: %w", err)
		}
	}

	broker := orchestrator.NewBroker()
	prov := pulumiengine.New(cfg.PulumiProject, cfg.PulumiBackend, passphrase)
	orch := orchestrator.New(st, prov, broker, cfg.Workers)
	if err := orch.Start(ctx); err != nil {
		return fmt.Errorf("start orchestrator: %w", err)
	}
	defer orch.Stop()

	deps := api.Deps{
		Store:        st,
		Validator:    cloud.NewValidator(),
		Auth:         auth.New(cfg.LoginPassword, sessionKey),
		Renderer:     renderer,
		Orchestrator: orch,
		Broker:       broker,
		Catalog:      cloud.NewCatalog(),
	}
	go api.WarmCatalogCache(ctx, deps)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// No global WriteTimeout: SSE streams are long-lived. Per-request
		// deadlines are managed where needed (login/forms are quick anyway).
		IdleTimeout: 60 * time.Second,
	}

	log.Printf("hermes listening on %s (workers=%d, backend=%s)", cfg.Addr, cfg.Workers, cfg.PulumiBackend)

	serverErrors := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		return fmt.Errorf("server: %w", err)
	}
	log.Print("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	// Cancel workers and wait until any interrupted job has persisted its failed
	// terminal state. The DB is closed only after Stop returns.
	// The DB is closed by the deferred st.Close() as main returns.
	return nil
}

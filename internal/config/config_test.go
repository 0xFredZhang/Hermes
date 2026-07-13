package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func validKeyB64() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32)) // 32 zero bytes
}

func TestLoadDefaultsToLoopback(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_ADDR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want 127.0.0.1:8080", cfg.Addr)
	}
}

func TestLoad(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", validKeyB64())
		t.Setenv("HERMES_LOGIN_PASSWORD", "secret")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.MasterKey) != 32 {
			t.Fatalf("MasterKey len = %d, want 32", len(cfg.MasterKey))
		}
		if cfg.LoginPassword != "secret" {
			t.Fatalf("LoginPassword = %q", cfg.LoginPassword)
		}
		if cfg.Addr != "127.0.0.1:8080" {
			t.Fatalf("Addr = %q, want default 127.0.0.1:8080", cfg.Addr)
		}
	})

	t.Run("wrong key length", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
		t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for 16-byte key, got nil")
		}
	})

	t.Run("missing password", func(t *testing.T) {
		t.Setenv("HERMES_MASTER_KEY", validKeyB64())
		t.Setenv("HERMES_LOGIN_PASSWORD", "")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for missing password, got nil")
		}
	})
}

func TestLoadProvisioningDefaults(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PulumiProject != "hermes" {
		t.Fatalf("PulumiProject = %q, want hermes", cfg.PulumiProject)
	}
	if cfg.Workers != 2 {
		t.Fatalf("Workers = %d, want 2", cfg.Workers)
	}
	if len(cfg.PulumiBackend) < 7 || cfg.PulumiBackend[:7] != "file://" {
		t.Fatalf("PulumiBackend = %q, want file:// default", cfg.PulumiBackend)
	}
}

func TestLoadDefaultBackendRoundTripsSpecialWorkingDirectory(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_PULUMI_BACKEND", "")
	workingDirectory := filepath.Join(t.TempDir(), "Hermes #1 ? 100%")
	if err := os.Mkdir(workingDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir working directory: %v", err)
	}
	t.Chdir(workingDirectory)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	path, isFile, err := LocalPulumiBackendPath(cfg.PulumiBackend)
	if err != nil {
		t.Fatalf("LocalPulumiBackendPath: %v", err)
	}
	if !isFile {
		t.Fatalf("PulumiBackend = %q, want local file backend", cfg.PulumiBackend)
	}
	want := filepath.Join(workingDirectory, "data", "pulumi-state")
	if path != want {
		t.Fatalf("local backend path = %q, want %q (URL %q)", path, want, cfg.PulumiBackend)
	}
}

func TestLoadAcceptsSupportedPulumiBackends(t *testing.T) {
	tests := []string{
		"file:///tmp/hermes-pulumi-state",
		"s3://hermes-state",
		"s3://hermes-state/team/dev",
	}
	for _, backend := range tests {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("HERMES_MASTER_KEY", validKeyB64())
			t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
			t.Setenv("HERMES_PULUMI_BACKEND", backend)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.PulumiBackend != backend {
				t.Fatalf("PulumiBackend = %q, want %q", cfg.PulumiBackend, backend)
			}
		})
	}
}

func TestLoadRejectsBadPulumiBackend(t *testing.T) {
	tests := []string{
		"data/pulumi-state",
		"file:data/pulumi-state",
		"file://",
		"file://data/pulumi-state",
		"file://localhost/tmp/pulumi-state",
		"file:///tmp/pulumi-state?mode=test",
		"file:///tmp/pulumi-state#backup",
		"file:///tmp/data/../pulumi-state",
		"file:///tmp/data/%2e%2e/pulumi-state",
		"s3://",
		"s3:///prefix-only",
		"azblob://hermes-state",
	}
	for _, backend := range tests {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("HERMES_MASTER_KEY", validKeyB64())
			t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
			t.Setenv("HERMES_PULUMI_BACKEND", backend)

			if _, err := Load(); err == nil {
				t.Fatalf("expected error for HERMES_PULUMI_BACKEND=%q", backend)
			}
		})
	}
}

func TestLoadWorkersOverride(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_WORKERS", "4")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != 4 {
		t.Fatalf("Workers = %d, want 4", cfg.Workers)
	}
}

func TestLoadRejectsBadWorkers(t *testing.T) {
	t.Setenv("HERMES_MASTER_KEY", validKeyB64())
	t.Setenv("HERMES_LOGIN_PASSWORD", "secret")
	t.Setenv("HERMES_WORKERS", "zero")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-numeric HERMES_WORKERS")
	}
}

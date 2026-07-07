package config

import (
	"encoding/base64"
	"testing"
)

func validKeyB64() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32)) // 32 zero bytes
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
		if cfg.Addr != ":8080" {
			t.Fatalf("Addr = %q, want default :8080", cfg.Addr)
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

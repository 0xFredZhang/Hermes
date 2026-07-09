package store

import (
	"context"
	"errors"
	"testing"
)

func TestEnvironmentSecretRoundTripsEncrypted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	secret := EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          SecretRDSMySQL,
		Username:      "admin",
		Password:      "generated-password",
		Metadata: map[string]any{
			"host":    "db.example",
			"port":    float64(3306),
			"db_name": "app",
		},
	}
	if err := s.UpsertEnvironmentSecret(ctx, secret); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}

	got, err := s.GetEnvironmentSecret(ctx, envID, SecretRDSMySQL)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if got.Username != "admin" || got.Password != "generated-password" {
		t.Fatalf("secret did not decrypt: %+v", got)
	}
	if got.Metadata["host"] != "db.example" || got.Metadata["db_name"] != "app" {
		t.Fatalf("metadata did not round-trip: %+v", got.Metadata)
	}

	var userEnc, passEnc string
	err = s.DB().QueryRow(
		`SELECT username_enc, password_enc FROM environment_secrets WHERE environment_id = ? AND kind = ?`,
		envID, SecretRDSMySQL,
	).Scan(&userEnc, &passEnc)
	if err != nil {
		t.Fatalf("query encrypted values: %v", err)
	}
	if userEnc == "admin" || passEnc == "generated-password" || userEnc == "" || passEnc == "" {
		t.Fatalf("secret stored in plaintext or empty: username=%q password=%q", userEnc, passEnc)
	}
}

func TestEnvironmentSecretUpsertReplacesExistingValue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	if err := s.UpsertEnvironmentSecret(ctx, EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          SecretRDSMySQL,
		Username:      "admin",
		Password:      "old-password",
	}); err != nil {
		t.Fatalf("first UpsertEnvironmentSecret: %v", err)
	}
	if err := s.UpsertEnvironmentSecret(ctx, EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          SecretRDSMySQL,
		Username:      "admin",
		Password:      "new-password",
	}); err != nil {
		t.Fatalf("second UpsertEnvironmentSecret: %v", err)
	}

	got, err := s.GetEnvironmentSecret(ctx, envID, SecretRDSMySQL)
	if err != nil {
		t.Fatalf("GetEnvironmentSecret: %v", err)
	}
	if got.Password != "new-password" {
		t.Fatalf("Password = %q, want new-password", got.Password)
	}
}

func TestGetEnvironmentSecretMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	_, err := s.GetEnvironmentSecret(ctx, envID, SecretRDSMySQL)
	if !errors.Is(err, ErrEnvironmentSecretNotFound) {
		t.Fatalf("GetEnvironmentSecret err = %v, want ErrEnvironmentSecretNotFound", err)
	}
}

func TestHasEnvironmentSecret(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	envID := seedEnvironment(t, s)

	has, err := s.HasEnvironmentSecret(ctx, envID, SecretRDSMySQL)
	if err != nil {
		t.Fatalf("HasEnvironmentSecret missing: %v", err)
	}
	if has {
		t.Fatal("HasEnvironmentSecret before insert = true, want false")
	}

	if err := s.UpsertEnvironmentSecret(ctx, EnvironmentSecret{
		EnvironmentID: envID,
		Kind:          SecretRDSMySQL,
		Username:      "admin",
		Password:      "generated-password",
	}); err != nil {
		t.Fatalf("UpsertEnvironmentSecret: %v", err)
	}
	has, err = s.HasEnvironmentSecret(ctx, envID, SecretRDSMySQL)
	if err != nil {
		t.Fatalf("HasEnvironmentSecret existing: %v", err)
	}
	if !has {
		t.Fatal("HasEnvironmentSecret after insert = false, want true")
	}
}

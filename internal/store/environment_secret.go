package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	SecretRDSMySQL  = "rds_mysql"
	SecretRedisAuth = "redis_auth"
)

var ErrEnvironmentSecretNotFound = errors.New("environment secret not found")

type EnvironmentSecret struct {
	ID            int64
	EnvironmentID int64
	Kind          string
	Username      string
	Password      string
	Metadata      map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (s *Store) UpsertEnvironmentSecret(ctx context.Context, secret EnvironmentSecret) error {
	usernameEnc, err := s.cipher.Encrypt(secret.Username)
	if err != nil {
		return err
	}
	passwordEnc, err := s.cipher.Encrypt(secret.Password)
	if err != nil {
		return err
	}
	metadata := secret.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	rawMetadata, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO environment_secrets (environment_id, kind, username_enc, password_enc, metadata_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(environment_id, kind) DO UPDATE SET
			username_enc = excluded.username_enc,
			password_enc = excluded.password_enc,
			metadata_json = excluded.metadata_json,
			updated_at = CURRENT_TIMESTAMP
	`, secret.EnvironmentID, secret.Kind, usernameEnc, passwordEnc, string(rawMetadata))
	return err
}

func (s *Store) GetEnvironmentSecret(ctx context.Context, envID int64, kind string) (EnvironmentSecret, error) {
	var secret EnvironmentSecret
	var usernameEnc, passwordEnc, metadata string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, environment_id, kind, username_enc, password_enc, metadata_json, created_at, updated_at
		FROM environment_secrets
		WHERE environment_id = ? AND kind = ?
	`, envID, kind).Scan(&secret.ID, &secret.EnvironmentID, &secret.Kind, &usernameEnc, &passwordEnc, &metadata, &secret.CreatedAt, &secret.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EnvironmentSecret{}, ErrEnvironmentSecretNotFound
	}
	if err != nil {
		return EnvironmentSecret{}, err
	}
	secret.Username, err = s.cipher.Decrypt(usernameEnc)
	if err != nil {
		return EnvironmentSecret{}, err
	}
	secret.Password, err = s.cipher.Decrypt(passwordEnc)
	if err != nil {
		return EnvironmentSecret{}, err
	}
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &secret.Metadata); err != nil {
			return EnvironmentSecret{}, err
		}
	}
	if secret.Metadata == nil {
		secret.Metadata = map[string]any{}
	}
	return secret, nil
}

func (s *Store) HasEnvironmentSecret(ctx context.Context, envID int64, kind string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM environment_secrets
		WHERE environment_id = ? AND kind = ?
		LIMIT 1
	`, envID, kind).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

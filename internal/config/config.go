package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	Addr          string
	DBPath        string
	MasterKey     []byte
	LoginPassword string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          envOr("HERMES_ADDR", ":8080"),
		DBPath:        envOr("HERMES_DB_PATH", "hermes.db"),
		LoginPassword: os.Getenv("HERMES_LOGIN_PASSWORD"),
	}

	rawKey := os.Getenv("HERMES_MASTER_KEY")
	if rawKey == "" {
		return Config{}, errors.New("HERMES_MASTER_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("HERMES_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.MasterKey = key

	if cfg.LoginPassword == "" {
		return Config{}, errors.New("HERMES_LOGIN_PASSWORD is required")
	}
	return cfg, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

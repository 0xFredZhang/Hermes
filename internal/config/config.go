package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr          string
	DBPath        string
	MasterKey     []byte
	LoginPassword string
	PulumiBackend string
	PulumiProject string
	Workers       int
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          envOr("HERMES_ADDR", ":8080"),
		DBPath:        envOr("HERMES_DB_PATH", "hermes.db"),
		LoginPassword: os.Getenv("HERMES_LOGIN_PASSWORD"),
		PulumiProject: envOr("HERMES_PULUMI_PROJECT", "hermes"),
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

	cfg.PulumiBackend = os.Getenv("HERMES_PULUMI_BACKEND")
	if cfg.PulumiBackend == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, err
		}
		cfg.PulumiBackend = "file://" + filepath.Join(cwd, "data", "pulumi-state")
	}
	if err := validatePulumiBackend(cfg.PulumiBackend); err != nil {
		return Config{}, err
	}

	cfg.Workers = 2
	if w := os.Getenv("HERMES_WORKERS"); w != "" {
		n, err := strconv.Atoi(w)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("HERMES_WORKERS must be a positive integer, got %q", w)
		}
		cfg.Workers = n
	}

	return cfg, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func validatePulumiBackend(backend string) error {
	if path, ok := strings.CutPrefix(backend, "file://"); ok {
		if path == "" {
			return errors.New("HERMES_PULUMI_BACKEND file:// URL requires a state directory path")
		}
		return nil
	}

	u, err := url.Parse(backend)
	if err != nil {
		return fmt.Errorf("HERMES_PULUMI_BACKEND is not a valid URL: %w", err)
	}
	if u.Scheme != "s3" {
		return fmt.Errorf("HERMES_PULUMI_BACKEND must use file:// or s3://, got %q", backend)
	}
	if u.Host == "" {
		return errors.New("HERMES_PULUMI_BACKEND s3:// URL requires a bucket name")
	}
	return nil
}

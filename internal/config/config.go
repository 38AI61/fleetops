// Package config loads runtime settings from the environment (README §12).
// Errors name the variable, never its value, so secrets stay out of logs.
package config

import (
	"errors"
	"fmt"
	"os"
)

// MinJWTSecretBytes is the shortest JWT_SECRET the process accepts (D-03).
const MinJWTSecretBytes = 32

// Config holds the settings the API and tools need at startup.
type Config struct {
	DatabaseURL string
	JWTSecret   string
}

// Load reads DATABASE_URL and JWT_SECRET from the environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	var cfg Config

	dsn, ok := lookup("DATABASE_URL")
	if !ok || dsn == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	cfg.DatabaseURL = dsn

	secret, ok := lookup("JWT_SECRET")
	if !ok || secret == "" {
		return Config{}, errors.New("JWT_SECRET is required")
	}
	if len(secret) < MinJWTSecretBytes {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least %d bytes", MinJWTSecretBytes)
	}
	cfg.JWTSecret = secret

	return cfg, nil
}

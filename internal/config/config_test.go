package config

import (
	"strings"
	"testing"
)

func fakeEnv(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestLoadAcceptsSecretOfExactlyMinimumLength(t *testing.T) {
	secret := strings.Repeat("s", 32)
	cfg, err := load(fakeEnv(map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db",
		"JWT_SECRET":   secret,
	}))
	if err != nil {
		t.Fatalf("32-byte secret: unexpected error %v", err)
	}
	if cfg.JWTSecret != secret || cfg.DatabaseURL != "postgres://u:p@h/db" {
		t.Fatalf("config not loaded: %+v", cfg)
	}
}

func TestLoadRefusesShortSecret(t *testing.T) {
	secret := strings.Repeat("s", 31)
	_, err := load(fakeEnv(map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db",
		"JWT_SECRET":   secret,
	}))
	if err == nil {
		t.Fatal("31-byte secret: want error, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("error should name JWT_SECRET: %v", err)
	}
}

func TestLoadRefusesMissingDatabaseURL(t *testing.T) {
	_, err := load(fakeEnv(map[string]string{
		"JWT_SECRET": strings.Repeat("s", 32),
	}))
	if err == nil {
		t.Fatal("missing DATABASE_URL: want error, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should name DATABASE_URL: %v", err)
	}
}

func TestLoadErrorNeverContainsSecret(t *testing.T) {
	secret := "hunter2-not-long-enough"
	_, err := load(fakeEnv(map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db",
		"JWT_SECRET":   secret,
	}))
	if err == nil {
		t.Fatal("short secret: want error, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks secret value: %v", err)
	}
}

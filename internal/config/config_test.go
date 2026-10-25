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

// validEnv returns a complete environment with the given salt.
func validEnv(salt string) map[string]string {
	return map[string]string{
		"DATABASE_URL":    "postgres://fleetops@db.invalid/fleetops",
		"JWT_SECRET":      strings.Repeat("s", 32),
		"DEVICE_KEY_SALT": salt,
		"NATS_URL":        "nats://nats.invalid:4222",
	}
}

func TestLoadAcceptsSecretOfExactlyMinimumLength(t *testing.T) {
	secret := strings.Repeat("s", 32)
	env := validEnv(strings.Repeat("d", 32))
	env["JWT_SECRET"] = secret
	cfg, err := load(fakeEnv(env))
	if err != nil {
		t.Fatalf("32-byte secret: unexpected error %v", err)
	}
	if cfg.JWTSecret != secret || cfg.DatabaseURL != "postgres://fleetops@db.invalid/fleetops" {
		t.Fatalf("config not loaded: %+v", cfg)
	}
}

func TestLoadRefusesShortSecret(t *testing.T) {
	env := validEnv(strings.Repeat("d", 32))
	env["JWT_SECRET"] = strings.Repeat("s", 31)
	_, err := load(fakeEnv(env))
	if err == nil {
		t.Fatal("31-byte secret: want error, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("error should name JWT_SECRET: %v", err)
	}
}

func TestLoadRefusesMissingDatabaseURL(t *testing.T) {
	_, err := load(fakeEnv(map[string]string{
		"JWT_SECRET":      strings.Repeat("s", 32),
		"DEVICE_KEY_SALT": strings.Repeat("d", 32),
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
	env := validEnv(strings.Repeat("d", 32))
	env["JWT_SECRET"] = secret
	_, err := load(fakeEnv(env))
	if err == nil {
		t.Fatal("short secret: want error, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks secret value: %v", err)
	}
}

func TestLoadAcceptsDeviceKeySaltOfExactlyMinimumLength(t *testing.T) {
	salt := strings.Repeat("d", 32)
	cfg, err := load(fakeEnv(validEnv(salt)))
	if err != nil {
		t.Fatalf("32-byte salt: unexpected error %v", err)
	}
	if cfg.DeviceKeySalt != salt {
		t.Fatalf("salt not loaded")
	}
}

func TestLoadRefusesShortDeviceKeySalt(t *testing.T) {
	_, err := load(fakeEnv(validEnv(strings.Repeat("d", 31))))
	if err == nil {
		t.Fatal("31-byte salt: want error, got nil")
	}
	if !strings.Contains(err.Error(), "DEVICE_KEY_SALT") {
		t.Errorf("error should name DEVICE_KEY_SALT: %v", err)
	}
}

func TestLoadRefusesMissingDeviceKeySalt(t *testing.T) {
	env := validEnv("")
	delete(env, "DEVICE_KEY_SALT")
	_, err := load(fakeEnv(env))
	if err == nil {
		t.Fatal("missing salt: want error, got nil")
	}
	if !strings.Contains(err.Error(), "DEVICE_KEY_SALT") {
		t.Errorf("error should name DEVICE_KEY_SALT: %v", err)
	}
}

func TestLoadDeviceKeySaltErrorNeverContainsValue(t *testing.T) {
	salt := "salt-value-that-is-too-short"
	_, err := load(fakeEnv(validEnv(salt)))
	if err == nil {
		t.Fatal("short salt: want error, got nil")
	}
	if strings.Contains(err.Error(), salt) {
		t.Fatalf("error leaks salt value: %v", err)
	}
}

func TestLoadAcceptsNATSURL(t *testing.T) {
	cfg, err := load(fakeEnv(validEnv(strings.Repeat("d", 32))))
	if err != nil {
		t.Fatalf("NATS_URL set: unexpected error %v", err)
	}
	if cfg.NATSURL != "nats://nats.invalid:4222" {
		t.Fatalf("NATS_URL not loaded: %q", cfg.NATSURL)
	}
}

func TestLoadRefusesMissingNATSURL(t *testing.T) {
	env := validEnv(strings.Repeat("d", 32))
	delete(env, "NATS_URL")
	_, err := load(fakeEnv(env))
	if err == nil {
		t.Fatal("missing NATS_URL: want error, got nil")
	}
	if !strings.Contains(err.Error(), "NATS_URL") {
		t.Errorf("error should name NATS_URL: %v", err)
	}
}

func TestLoadRefusesEmptyNATSURL(t *testing.T) {
	env := validEnv(strings.Repeat("d", 32))
	env["NATS_URL"] = ""
	if _, err := load(fakeEnv(env)); err == nil {
		t.Fatal("empty NATS_URL: want error, got nil")
	}
}

package config

import (
	"strings"
	"testing"
	"time"
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

func TestLoadFlushDefaultsWhenUnset(t *testing.T) {
	cfg, err := load(fakeEnv(validEnv(strings.Repeat("d", 32))))
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if cfg.FlushInterval != time.Second || cfg.FlushMax != 500 || cfg.AccuracyMaxM != 50 {
		t.Fatalf("flush defaults: got %v %d %v", cfg.FlushInterval, cfg.FlushMax, cfg.AccuracyMaxM)
	}
}

func TestLoadFlushAcceptsOverrides(t *testing.T) {
	env := validEnv(strings.Repeat("d", 32))
	env["FLEETOPS_FLUSH_INTERVAL"] = "250ms"
	env["FLEETOPS_FLUSH_MAX"] = "200"
	env["FLEETOPS_ACCURACY_MAX_M"] = "12.5"
	cfg, err := load(fakeEnv(env))
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if cfg.FlushInterval != 250*time.Millisecond || cfg.FlushMax != 200 || cfg.AccuracyMaxM != 12.5 {
		t.Fatalf("flush overrides: got %v %d %v", cfg.FlushInterval, cfg.FlushMax, cfg.AccuracyMaxM)
	}
}

func TestLoadFlushEmptyIsDefault(t *testing.T) {
	env := validEnv(strings.Repeat("d", 32))
	env["FLEETOPS_FLUSH_INTERVAL"] = ""
	env["FLEETOPS_FLUSH_MAX"] = ""
	env["FLEETOPS_ACCURACY_MAX_M"] = ""
	cfg, err := load(fakeEnv(env))
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if cfg.FlushInterval != time.Second || cfg.FlushMax != 500 || cfg.AccuracyMaxM != 50 {
		t.Fatalf("empty values should yield defaults: got %v %d %v", cfg.FlushInterval, cfg.FlushMax, cfg.AccuracyMaxM)
	}
}

func TestLoadFlushRefusesInvalid(t *testing.T) {
	cases := []struct{ name, value string }{
		{"FLEETOPS_FLUSH_INTERVAL", "abc"},
		{"FLEETOPS_FLUSH_INTERVAL", "0s"},
		{"FLEETOPS_FLUSH_INTERVAL", "50ms"},
		{"FLEETOPS_FLUSH_INTERVAL", "11s"},
		{"FLEETOPS_FLUSH_MAX", "0"},
		{"FLEETOPS_FLUSH_MAX", "5001"},
		{"FLEETOPS_FLUSH_MAX", "-1"},
		{"FLEETOPS_FLUSH_MAX", "1.5"},
		{"FLEETOPS_ACCURACY_MAX_M", "0"},
		{"FLEETOPS_ACCURACY_MAX_M", "-3"},
		{"FLEETOPS_ACCURACY_MAX_M", "NaN"},
		{"FLEETOPS_ACCURACY_MAX_M", "Inf"},
		{"FLEETOPS_ACCURACY_MAX_M", "10000.5"},
	}
	for _, tc := range cases {
		env := validEnv(strings.Repeat("d", 32))
		env[tc.name] = tc.value
		if _, err := load(fakeEnv(env)); err == nil {
			t.Errorf("%s=%q: want error, got none", tc.name, tc.value)
		}
	}
}

func TestLoadFlushErrorNeverContainsValue(t *testing.T) {
	const bad = "bogus-7731"
	for _, name := range []string{"FLEETOPS_FLUSH_INTERVAL", "FLEETOPS_FLUSH_MAX", "FLEETOPS_ACCURACY_MAX_M"} {
		env := validEnv(strings.Repeat("d", 32))
		env[name] = bad
		_, err := load(fakeEnv(env))
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error %q contains the offending value", err)
		}
	}
}

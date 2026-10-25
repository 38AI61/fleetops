// Package config loads runtime settings from the environment (README §12).
// Errors name the variable, never its value, so secrets stay out of logs.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// MinJWTSecretBytes is the shortest JWT_SECRET the process accepts (D-03).
const MinJWTSecretBytes = 32

// MinDeviceKeySaltBytes is the shortest DEVICE_KEY_SALT the process accepts (GF-30).
const MinDeviceKeySaltBytes = 32

// Optional FLEETOPS_* tunables (D-22, GF-80). Defaults apply when the variable
// is unset or empty; out-of-range values are refused.
const (
	DefaultFlushInterval = time.Second
	DefaultFlushMax      = 500
	DefaultAccuracyMaxM  = 50.0

	minFlushInterval = 100 * time.Millisecond
	maxFlushInterval = 10 * time.Second
	minFlushMax      = 1
	maxFlushMax      = 5000
	maxAccuracyMaxM  = 10000.0
)

// Config holds the settings the API and tools need at startup.
type Config struct {
	DatabaseURL   string
	JWTSecret     string
	DeviceKeySalt string
	NATSURL       string

	FlushInterval time.Duration
	FlushMax      int
	AccuracyMaxM  float64
}

// Load reads DATABASE_URL, JWT_SECRET, DEVICE_KEY_SALT, and NATS_URL from the environment.
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

	salt, ok := lookup("DEVICE_KEY_SALT")
	if !ok || salt == "" {
		return Config{}, errors.New("DEVICE_KEY_SALT is required")
	}
	if len(salt) < MinDeviceKeySaltBytes {
		return Config{}, fmt.Errorf("DEVICE_KEY_SALT must be at least %d bytes", MinDeviceKeySaltBytes)
	}
	cfg.DeviceKeySalt = salt

	natsURL, ok := lookup("NATS_URL")
	if !ok || natsURL == "" {
		return Config{}, errors.New("NATS_URL is required")
	}
	cfg.NATSURL = natsURL

	if err := loadFlush(lookup, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// loadFlush reads the three optional batch-writer tunables (GF-80). Each error
// names the variable and never the value.
func loadFlush(lookup func(string) (string, bool), cfg *Config) error {
	cfg.FlushInterval = DefaultFlushInterval
	cfg.FlushMax = DefaultFlushMax
	cfg.AccuracyMaxM = DefaultAccuracyMaxM

	if v, ok := lookup("FLEETOPS_FLUSH_INTERVAL"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minFlushInterval || d > maxFlushInterval {
			return fmt.Errorf("FLEETOPS_FLUSH_INTERVAL must be a duration from %s to %s", minFlushInterval, maxFlushInterval)
		}
		cfg.FlushInterval = d
	}

	if v, ok := lookup("FLEETOPS_FLUSH_MAX"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minFlushMax || n > maxFlushMax {
			return fmt.Errorf("FLEETOPS_FLUSH_MAX must be an integer from %d to %d", minFlushMax, maxFlushMax)
		}
		cfg.FlushMax = n
	}

	if v, ok := lookup("FLEETOPS_ACCURACY_MAX_M"); ok && v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > maxAccuracyMaxM {
			return fmt.Errorf("FLEETOPS_ACCURACY_MAX_M must be a finite number greater than 0 and at most %g", maxAccuracyMaxM)
		}
		cfg.AccuracyMaxM = f
	}

	return nil
}

package config

import (
	"strings"
	"testing"
)

// TestLoadFanoutMode covers GF-347: the mode default, the nats mode without
// REDIS_URL, the redis mode that needs it, and the error text for a bad value.
func TestLoadFanoutMode(t *testing.T) {
	base := func() map[string]string {
		env := validEnv(strings.Repeat("d", 32))
		delete(env, "REDIS_URL")
		return env
	}
	withRedis := func(env map[string]string, mode string) map[string]string {
		env["REDIS_URL"] = "redis://redis.invalid:6379"
		env["FANOUT_MODE"] = mode
		return env
	}

	t.Run("unset defaults to redis", func(t *testing.T) {
		cfg, err := load(fakeEnv(withRedis(base(), "")))
		if err != nil || cfg.FanoutMode != FanoutRedis {
			t.Fatalf("mode %q err %v, want redis", cfg.FanoutMode, err)
		}
	})
	t.Run("nats without REDIS_URL is accepted", func(t *testing.T) {
		env := base()
		env["FANOUT_MODE"] = FanoutNATS
		cfg, err := load(fakeEnv(env))
		if err != nil || cfg.FanoutMode != FanoutNATS || cfg.RedisURL != "" {
			t.Fatalf("mode %q redis %q err %v", cfg.FanoutMode, cfg.RedisURL, err)
		}
	})
	t.Run("redis without REDIS_URL is refused", func(t *testing.T) {
		env := base()
		env["FANOUT_MODE"] = FanoutRedis
		_, err := load(fakeEnv(env))
		if err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
			t.Fatalf("err %v, want REDIS_URL error", err)
		}
	})
	t.Run("nats with a malformed REDIS_URL is refused", func(t *testing.T) {
		env := base()
		env["FANOUT_MODE"] = FanoutNATS
		env["REDIS_URL"] = "http://redis.invalid"
		_, err := load(fakeEnv(env))
		if err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
			t.Fatalf("err %v, want REDIS_URL error", err)
		}
	})
	t.Run("bogus mode names the variable, not the value", func(t *testing.T) {
		env := withRedis(base(), "bogus-secret-value")
		_, err := load(fakeEnv(env))
		if err == nil {
			t.Fatal("bogus mode accepted")
		}
		if !strings.Contains(err.Error(), "FANOUT_MODE") || strings.Contains(err.Error(), "bogus-secret-value") {
			t.Fatalf("err %q must name FANOUT_MODE and not echo the value", err)
		}
	})
}

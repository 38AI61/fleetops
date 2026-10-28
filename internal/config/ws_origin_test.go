package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestWSOriginPatternsFromEnv (GF-353): unset keeps the defaults, a list is
// trimmed, and an empty entry is refused with the variable named.
func TestWSOriginPatternsFromEnv(t *testing.T) {
	t.Run("unset keeps the hub defaults", func(t *testing.T) {
		cfg, err := load(fakeEnv(validEnv(strings.Repeat("d", 32))))
		if err != nil || cfg.WSOriginPatterns != nil {
			t.Fatalf("patterns %v err %v, want nil", cfg.WSOriginPatterns, err)
		}
	})
	t.Run("list is trimmed", func(t *testing.T) {
		env := validEnv(strings.Repeat("d", 32))
		env["WS_ORIGIN_PATTERNS"] = " fleetops.pages.dev , *.fleetops.pages.dev"
		cfg, err := load(fakeEnv(env))
		want := []string{"fleetops.pages.dev", "*.fleetops.pages.dev"}
		if err != nil || !reflect.DeepEqual(cfg.WSOriginPatterns, want) {
			t.Fatalf("patterns %v err %v, want %v", cfg.WSOriginPatterns, err, want)
		}
	})
	t.Run("empty entry is refused", func(t *testing.T) {
		for _, v := range []string{"a.example,,b.example", "a.example, ", ","} {
			env := validEnv(strings.Repeat("d", 32))
			env["WS_ORIGIN_PATTERNS"] = v
			_, err := load(fakeEnv(env))
			if err == nil || !strings.Contains(err.Error(), "WS_ORIGIN_PATTERNS") {
				t.Fatalf("%q: err %v, want WS_ORIGIN_PATTERNS refusal", v, err)
			}
		}
	})
}

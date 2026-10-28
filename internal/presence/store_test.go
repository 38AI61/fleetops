package presence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// TestMemoryStoreExpiresAfterTTL (GF-347) uses an injected clock: a key is live
// until its TTL instant and absent after it.
func TestMemoryStoreExpiresAfterTTL(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := NewMemoryStore(clock)
	ctx := context.Background()
	ttl := 30 * time.Second

	if _, found, _ := s.SetGet(ctx, "device:1", stateOnline, ttl); found {
		t.Fatal("first write reported a previous value")
	}
	now = now.Add(ttl - time.Second)
	if prev, found, _ := s.SetGet(ctx, "device:1", stateOnline, ttl); !found || prev != stateOnline {
		t.Fatalf("inside TTL: prev %q found %v, want online true", prev, found)
	}
	now = now.Add(ttl)
	if _, found, _ := s.SetGet(ctx, "device:1", stateOnline, ttl); found {
		t.Fatal("key still live after TTL")
	}
}

// TestMemoryStoreMatchesRedisWithinTTL (property, D-37): random key/state
// sequences with no expiry crossing give the same (prev, found) as Redis.
func TestMemoryStoreMatchesRedisWithinTTL(t *testing.T) {
	opts, err := redis.ParseURL(testinfra.NewRedisURL(t))
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	ttl := time.Hour
	run := 0

	rapid.Check(t, func(rt *rapid.T) {
		run++
		prefix := fmt.Sprintf("parity:%d:", run)
		mem := NewMemoryStore(time.Now)
		red := NewRedisStore(rdb)
		ops := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) [2]string {
			return [2]string{
				rapid.SampledFrom([]string{"device:1", "device:2", "device:3"}).Draw(rt, "key"),
				rapid.SampledFrom([]string{stateOnline, stateOffline}).Draw(rt, "state"),
			}
		}), 1, 40).Draw(rt, "ops")
		for i, op := range ops {
			key := prefix + op[0]
			wantPrev, wantFound, err := mem.SetGet(ctx, key, op[1], ttl)
			if err != nil {
				rt.Fatalf("memory op %d: %v", i, err)
			}
			gotPrev, gotFound, err := red.SetGet(ctx, key, op[1], ttl)
			if err != nil {
				rt.Fatalf("redis op %d: %v", i, err)
			}
			if wantPrev != gotPrev || wantFound != gotFound {
				rt.Fatalf("op %d %s=%s: memory (%q,%v) redis (%q,%v)", i, key, op[1], wantPrev, wantFound, gotPrev, gotFound)
			}
		}
		rdb.Del(ctx, keysFor(prefix, ops)...)
	})
}

func keysFor(prefix string, ops [][2]string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, op := range ops {
		k := prefix + op[0]
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

package presence

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store holds the TTL'd presence keys (GF-347). RedisStore is the default and
// MemoryStore backs nats mode, where the services share no Redis.
type Store interface {
	// SetGet writes state with ttl and returns the previous value. found is
	// false when the key was absent or expired. It matches SET … EX ttl GET.
	SetGet(ctx context.Context, key, state string, ttl time.Duration) (prev string, found bool, err error)
}

// RedisStore is the SET … EX … GET store (GF-104).
type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore wraps an open client. The caller owns its lifetime.
func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb}
}

// SetGet runs one SET with EX and GET. redis.Nil on an absent key is a result,
// not an error.
func (s *RedisStore) SetGet(ctx context.Context, key, state string, ttl time.Duration) (string, bool, error) {
	prev, err := s.rdb.SetArgs(ctx, key, state, redis.SetArgs{TTL: ttl, Get: true}).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return prev, true, nil
}

// MemoryStore is a mutex-guarded map with expiry checked on read (GF-347). It
// lives in one process, so it is correct only where one process runs presence,
// which is the single-instance cloud profile.
type MemoryStore struct {
	now func() time.Time

	mu   sync.Mutex
	keys map[string]memEntry
}

type memEntry struct {
	state   string
	expires time.Time
}

// NewMemoryStore returns an empty store. now is the clock; tests inject one.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	return &MemoryStore{now: now, keys: map[string]memEntry{}}
}

// SetGet mirrors RedisStore: a key is live until its expiry instant, and the
// write refreshes the expiry. The error is always nil.
func (s *MemoryStore) SetGet(_ context.Context, key, state string, ttl time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	prev, ok := s.keys[key]
	s.keys[key] = memEntry{state: state, expires: now.Add(ttl)}
	if !ok || !now.Before(prev.expires) {
		return "", false, nil
	}
	return prev.state, true, nil
}

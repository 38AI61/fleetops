package testinfra

import (
	"context"
	"sync"
	"testing"
	"time"

	redismodule "github.com/testcontainers/testcontainers-go/modules/redis"
)

// RedisImage is the Redis server image used by tests (GF-122).
const RedisImage = "redis:7"

// redisShared holds the one Redis container a test process uses (GF-122).
var redisShared struct {
	once      sync.Once
	ctr       *redismodule.RedisContainer
	url       string
	err       error
	terminate sync.Once
}

// NewRedisURL starts the shared container on first use and returns its URL.
// Tests use unique device and fleet ids, so the container is not flushed
// between tests (GF-122).
func NewRedisURL(t testing.TB) string {
	t.Helper()
	redisShared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		redisShared.ctr, redisShared.err = redismodule.Run(ctx, RedisImage)
		if redisShared.err != nil {
			return
		}
		redisShared.url, redisShared.err = redisShared.ctr.ConnectionString(ctx)
	})
	if redisShared.err != nil {
		t.Fatalf("start %s (is Docker running?): %v", RedisImage, redisShared.err)
	}
	return redisShared.url
}

// TerminateRedis removes the shared Redis container. Call it from TestMain
// after m.Run so the container does not outlive the test process (GF-122).
func TerminateRedis() {
	redisShared.terminate.Do(func() {
		if redisShared.ctr == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = redisShared.ctr.Terminate(ctx)
	})
}

package ratelimit

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Integration tests need a live Redis; set REDIS_TEST_URL to run them
// (locally: redis://127.0.0.1:6379 — the compose service).
func redisTestClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("REDIS_TEST_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return rdb
}

func TestRedisSharedBucketsAcrossInstances(t *testing.T) {
	rdb := redisTestClient(t)
	key := fmt.Sprintf("test:%d", time.Now().UnixNano())
	a := NewRedis(rdb, PerMinute(60, 2), "t") // 1/s, burst 2
	b := NewRedis(rdb, PerMinute(60, 2), "t")

	// Two independent limiters must share one bucket.
	if ok, _ := a.Allow(key); !ok {
		t.Fatal("first allow denied")
	}
	if ok, _ := b.Allow(key); !ok {
		t.Fatal("second instance should see shared bucket")
	}
	if ok, ra := b.Allow(key); ok || ra < time.Second {
		t.Fatalf("expected shared denial, got ok=%v ra=%v", ok, ra)
	}
}

func TestRedisDenyDoesNotConsume(t *testing.T) {
	rdb := redisTestClient(t)
	key := fmt.Sprintf("test:%d", time.Now().UnixNano())
	l := NewRedis(rdb, PerMinute(60, 1), "t") // 1/s, burst 1

	l.Hit(key) // drain the single token
	if denied, _ := l.Deny(key); !denied {
		t.Fatal("expected deny after bucket drained")
	}
	if ok, _ := l.Allow(key); ok {
		t.Fatal("Deny must not consume a token")
	}
	// Still denied — nothing was consumed by the Deny calls.
	if denied, _ := l.Deny(key); !denied {
		t.Fatal("bucket should still be empty")
	}
}

func TestRedisNamespacesAreIsolated(t *testing.T) {
	rdb := redisTestClient(t)
	key := fmt.Sprintf("test:%d", time.Now().UnixNano())
	read := NewRedis(rdb, PerMinute(60, 1), "read")
	write := NewRedis(rdb, PerMinute(60, 1), "write")

	read.Hit(key)
	if ok, _ := write.Allow(key); !ok {
		t.Fatal("different namespaces must not share buckets")
	}
}

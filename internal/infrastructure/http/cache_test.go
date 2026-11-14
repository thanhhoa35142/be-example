package http

import (
	"fmt"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
)

func cacheTestRedis(t *testing.T) *redis.Client {
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

func cacheRouter(rdb *redis.Client, calls *atomic.Int32) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/spots", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true}), func(c *gin.Context) {
		calls.Add(1)
		c.JSON(200, gin.H{"n": calls.Load()})
	})
	r.GET("/v1/err", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true}), func(c *gin.Context) {
		c.JSON(500, gin.H{"err": true})
	})
	return r
}

func TestCacheHitSkipsHandler(t *testing.T) {
	rdb := cacheTestRedis(t)
	var calls atomic.Int32
	r := cacheRouter(rdb, &calls)
	_ = rdb.Set(t.Context(), cache.VersionKey, "1", 0).Err()

	for i, want := range []string{"MISS", "HIT"} {
		w := do(r, "GET", "/v1/spots?bbox=1,2,3,4", "1.2.3.4", nil, "")
		if w.Code != 200 {
			t.Fatalf("req %d: status %d", i, w.Code)
		}
		if got := w.Header().Get("X-Cache"); got != want {
			t.Fatalf("req %d: X-Cache=%q want %q", i, got, want)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
}

func TestCacheVersionBumpInvalidates(t *testing.T) {
	rdb := cacheTestRedis(t)
	var calls atomic.Int32
	r := cacheRouter(rdb, &calls)
	ns := fmt.Sprintf("%d", time.Now().UnixNano())
	_ = rdb.Set(t.Context(), cache.VersionKey, ns, 0).Err()

	path := "/v1/spots?bbox=5,6,7,8"
	w1 := do(r, "GET", path, "1.2.3.4", nil, "")
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatal("first request should miss")
	}
	w2 := do(r, "GET", path, "1.2.3.4", nil, "")
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatal("second request should hit")
	}
	if _, err := rdb.Incr(t.Context(), cache.VersionKey).Result(); err != nil {
		t.Fatalf("bump: %v", err)
	}
	w3 := do(r, "GET", path, "1.2.3.4", nil, "")
	if w3.Header().Get("X-Cache") != "MISS" {
		t.Fatal("post-bump request should miss")
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}

func TestCacheSkipsNon200AndNilRedis(t *testing.T) {
	rdb := cacheTestRedis(t)
	var calls atomic.Int32
	r := cacheRouter(rdb, &calls)

	w := do(r, "GET", "/v1/err", "1.2.3.4", nil, "")
	if w.Code != 500 {
		t.Fatalf("status %d", w.Code)
	}
	if w.Header().Get("X-Cache") != "" {
		t.Fatal("errors must not be cached")
	}

	// Nil client: middleware must pass through untouched.
	gin.SetMode(gin.TestMode)
	r2 := gin.New()
	var n2 atomic.Int32
	r2.GET("/v1/spots", Cache(nil, CacheOptions{TTL: time.Minute}), func(c *gin.Context) {
		n2.Add(1)
		c.JSON(200, gin.H{"n": n2.Load()})
	})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/spots", nil)
		r2.ServeHTTP(w, req)
		if w.Header().Get("X-Cache") != "" {
			t.Fatal("nil redis must not set X-Cache")
		}
	}
	if n2.Load() != 2 {
		t.Fatalf("nil-redis handler ran %d times, want 2", n2.Load())
	}
}

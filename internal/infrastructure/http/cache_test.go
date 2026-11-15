package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	uniq(t, rdb)

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

func TestCacheHitPreservesHeaders(t *testing.T) {
	rdb := cacheTestRedis(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/packs", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Normalize: DropQueryParams}), func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=3600")
		c.JSON(200, gin.H{"data": gin.H{"items": []int{1}}})
	})
	uniq(t, rdb)

	for i, want := range []string{"MISS", "HIT"} {
		w := do(r, "GET", "/v1/packs?utm=junk", "1.2.3.4", nil, "")
		if w.Code != 200 {
			t.Fatalf("req %d: status %d", i, w.Code)
		}
		if got := w.Header().Get("X-Cache"); got != want {
			t.Fatalf("req %d: X-Cache=%q want %q", i, got, want)
		}
	}

	w := do(r, "GET", "/v1/packs?other=1", "1.2.3.4", nil, "")
	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("junk params must share the entry, X-Cache=%q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control=%q, want stored value replayed", got)
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

	if got := w.Header().Get("X-Cache"); got != "BYPASS" {
		t.Fatalf("X-Cache=%q want BYPASS", got)
	}

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

func uniq(t *testing.T, rdb *redis.Client) string {
	t.Helper()
	ns := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := rdb.Set(t.Context(), cache.VersionKey, ns, 0).Err(); err != nil {
		t.Fatal(err)
	}
	return ns
}

func routeOf(t *testing.T, st *CacheStats, route string) RouteCacheStats {
	t.Helper()
	for _, r := range st.Snapshot().Routes {
		if r.Route == route {
			return r
		}
	}
	t.Fatalf("no stats for %s: %+v", route, st.Snapshot())
	return RouteCacheStats{}
}

func newReq(path string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "1.2.3.4:1"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

func TestCacheStoresSlowHandlerWithoutTrippingBreaker(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var calls atomic.Int32
	r.GET("/slow", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) {
		calls.Add(1)
		time.Sleep(cacheTimeout + 150*time.Millisecond)
		c.JSON(200, gin.H{"slow": true})
	})
	r.GET("/fast", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) {
		c.JSON(200, gin.H{"fast": true})
	})

	w1 := do(r, "GET", "/slow", "1.2.3.4", nil, "")
	w2 := do(r, "GET", "/slow", "1.2.3.4", nil, "")
	if w1.Header().Get("X-Cache") != "MISS" || w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("slow response not stored: %q then %q", w1.Header().Get("X-Cache"), w2.Header().Get("X-Cache"))
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times", calls.Load())
	}

	if got := do(r, "GET", "/fast", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("breaker open after slow handler: X-Cache=%q", got)
	}
	if got := do(r, "GET", "/fast", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("X-Cache=%q want HIT", got)
	}
	if s := routeOf(t, st, "/slow"); s.BreakerTrips != 0 || s.StoreErrors != 0 || s.Hit != 1 || s.Miss != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestCacheClientCancelDoesNotTripBreaker(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/c", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := newReq("/c", nil).WithContext(ctx)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if s := routeOf(t, st, "/c"); s.BreakerTrips != 0 || s.BypassByReason["canceled"] != 1 {
		t.Fatalf("cancel misclassified: %+v", s)
	}

	if got := do(r, "GET", "/c", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("breaker open after client cancel: X-Cache=%q", got)
	}
}

func TestCacheBreakerOnlyOnRealRedisFailure(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var calls atomic.Int32
	r.GET("/d", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) {
		calls.Add(1)
		c.JSON(200, gin.H{"ok": true})
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ServeHTTP(httptest.NewRecorder(), newReq("/d", nil).WithContext(ctx))
	if s := routeOf(t, st, "/d"); s.BreakerTrips != 0 || s.BypassByReason["canceled"] != 1 {
		t.Fatalf("cancel tripped the breaker: %+v", s)
	}

	if w := do(r, "GET", "/d", "1.2.3.4", nil, ""); w.Code != 200 {
		t.Fatalf("outage must fail open, got %d", w.Code)
	}
	do(r, "GET", "/d", "1.2.3.4", nil, "")
	s := routeOf(t, st, "/d")
	if s.BreakerTrips != 1 || s.BypassByReason["redis"] != 1 || s.BypassByReason["breaker"] != 1 {
		t.Fatalf("outage stats: %+v", s)
	}
	if calls.Load() != 3 {
		t.Fatalf("handler ran %d times, want 3", calls.Load())
	}
}

func TestCacheXCacheHonestAndCounted(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mw := func() gin.HandlerFunc {
		return Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st})
	}
	r.GET("/ok", mw(), func(c *gin.Context) { c.JSON(200, gin.H{"ok": 1}) })
	r.GET("/500", mw(), func(c *gin.Context) { c.JSON(500, gin.H{"e": 1}) })
	r.GET("/html", mw(), func(c *gin.Context) { c.Data(200, "text/html", []byte("<p>x</p>")) })
	r.GET("/204", mw(), func(c *gin.Context) { c.AbortWithStatus(204) })
	r.GET("/big", mw(), func(c *gin.Context) {
		c.JSON(200, gin.H{"pad": strings.Repeat("x", maxCacheEntry+10)})
	})
	for path, want := range map[string]string{
		"/ok": "MISS", "/500": "BYPASS", "/html": "BYPASS", "/204": "BYPASS", "/big": "BYPASS",
	} {
		w := do(r, "GET", path, "1.2.3.4", nil, "")
		if got := w.Header().Get("X-Cache"); got != want {
			t.Errorf("%s: X-Cache=%q want %q (status %d)", path, got, want, w.Code)
		}
	}
	if w := do(r, "GET", "/204", "1.2.3.4", nil, ""); w.Code != 204 {
		t.Errorf("AbortWithStatus(204) -> %d", w.Code)
	}
	if got := do(r, "GET", "/ok", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("/ok second: %q", got)
	}
	for route, want := range map[string]map[string]uint64{
		"/500": {"status": 1}, "/html": {"content": 1}, "/big": {"size": 1}, "/204": {"status": 2},
	} {
		got := routeOf(t, st, route).BypassByReason
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s bypass[%s]=%d want %d (%v)", route, k, got[k], v, got)
			}
		}
	}
	if s := routeOf(t, st, "/ok"); s.Miss != 1 || s.Hit != 1 || s.HitRate == nil || *s.HitRate != 0.5 {
		t.Errorf("/ok stats %+v", s)
	}
}

func bigJSON() gin.H {
	items := make([]string, 400)
	for i := range items {
		items[i] = fmt.Sprintf("item-%04d", i)
	}
	return gin.H{"items": items}
}

func gunzipBody(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func TestCacheHitServesPrecompressedGzip(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Gzip())
	var calls atomic.Int32
	r.GET("/g", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: NewCacheStats()}), func(c *gin.Context) {
		calls.Add(1)
		c.Header("Cache-Control", "public, max-age=60")
		c.JSON(200, bigJSON())
	})
	gz := map[string]string{"Accept-Encoding": "gzip"}

	miss := do(r, "GET", "/g", "1.2.3.4", gz, "")
	hit := do(r, "GET", "/g", "1.2.3.4", gz, "")
	plain := do(r, "GET", "/g", "1.2.3.4", nil, "")
	if miss.Header().Get("X-Cache") != "MISS" || hit.Header().Get("X-Cache") != "HIT" || plain.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache: %q %q %q", miss.Header().Get("X-Cache"), hit.Header().Get("X-Cache"), plain.Header().Get("X-Cache"))
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times", calls.Load())
	}
	for name, w := range map[string]*httptest.ResponseRecorder{"miss": miss, "hit": hit} {
		if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("%s headers: %v", name, w.Header())
		}
	}
	if got := hit.Header().Get("Content-Length"); got != fmt.Sprint(hit.Body.Len()) {
		t.Fatalf("hit Content-Length=%s body=%d", got, hit.Body.Len())
	}
	if hit.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("stored headers lost: %v", hit.Header())
	}
	want := plain.Body.Bytes()
	if plain.Header().Get("Content-Encoding") != "" || plain.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("identity client headers: %v", plain.Header())
	}
	if got := gunzipBody(t, hit.Body.Bytes()); !bytes.Equal(got, want) {
		t.Fatal("gzip hit body differs from identity body (double compression?)")
	}
	if got := gunzipBody(t, miss.Body.Bytes()); !bytes.Equal(got, want) {
		t.Fatal("gzip miss body differs from identity body")
	}
	if hit.Body.Len() >= len(want) {
		t.Fatalf("stored copy not compressed: %d >= %d", hit.Body.Len(), len(want))
	}
}

func TestCacheCoalescesConcurrentMisses(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var calls atomic.Int32
	r.GET("/s", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) {
		calls.Add(1)
		time.Sleep(200 * time.Millisecond)
		c.JSON(200, bigJSON())
	})
	const n = 24
	var wg sync.WaitGroup
	codes := make([]int, n)
	xc := make([]string, n)
	bodies := make([][]byte, n)
	enc := make([]string, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			hdr := map[string]string{}
			if i%2 == 0 {
				hdr["Accept-Encoding"] = "gzip"
			}
			w := do(r, "GET", "/s", "1.2.3.4", hdr, "")
			codes[i], xc[i], enc[i] = w.Code, w.Header().Get("X-Cache"), w.Header().Get("Content-Encoding")
			bodies[i] = w.Body.Bytes()
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times for %d concurrent requests", calls.Load(), n)
	}
	var want []byte
	counts := map[string]int{}
	for i := range codes {
		if codes[i] != 200 {
			t.Fatalf("req %d: %d", i, codes[i])
		}
		counts[xc[i]]++
		b := bodies[i]
		if enc[i] == "gzip" {
			b = gunzipBody(t, b)
		}
		if want == nil {
			want = b
		} else if !bytes.Equal(want, b) {
			t.Fatalf("req %d body differs", i)
		}
	}
	if counts["MISS"] != 1 || counts["COALESCED"]+counts["HIT"] != n-1 {
		t.Fatalf("X-Cache distribution %v", counts)
	}
	if s := routeOf(t, st, "/s"); s.Miss != 1 || s.Coalesced+s.Hit != n-1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestCacheLeaderPanicDoesNotPoisonWaiters(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Recovery())
	var calls atomic.Int32
	r.GET("/p", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: NewCacheStats()}), func(c *gin.Context) {
		if calls.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond)
			panic("boom")
		}
		c.JSON(200, gin.H{"ok": true})
	})
	const n = 6
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = do(r, "GET", "/p", "1.2.3.4", nil, "").Code
		}()
	}
	wg.Wait()
	got500, got200 := 0, 0
	for _, c := range codes {
		switch c {
		case 500:
			got500++
		case 200:
			got200++
		}
	}
	if got500 != 1 || got200 != n-1 {
		t.Fatalf("codes %v: want exactly the leader to fail", codes)
	}
}

func TestCacheWeatherVersionScope(t *testing.T) {
	rdb := cacheTestRedis(t)
	uniq(t, rdb)
	_ = rdb.Del(t.Context(), cache.WeatherVersionKey).Err()
	st := NewCacheStats()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/scores", Cache(rdb, CacheOptions{TTL: time.Minute, Versioned: true, Stats: st}), func(c *gin.Context) { c.JSON(200, gin.H{"a": 1}) })
	r.GET("/forecast", Cache(rdb, CacheOptions{TTL: time.Minute, WeatherVersioned: true, Stats: st}), func(c *gin.Context) { c.JSON(200, gin.H{"b": 1}) })
	for _, p := range []string{"/scores", "/forecast"} {
		do(r, "GET", p, "1.2.3.4", nil, "")
		if got := do(r, "GET", p, "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "HIT" {
			t.Fatalf("%s warmup: %q", p, got)
		}
	}
	if err := cache.BumpWeather(t.Context(), rdb); err != nil {
		t.Fatal(err)
	}
	if got := do(r, "GET", "/scores", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("weather bump flushed scores route: %q", got)
	}
	if got := do(r, "GET", "/forecast", "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("weather bump kept forecast entry: %q", got)
	}
	if err := cache.Bump(t.Context(), rdb); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/scores", "/forecast"} {
		if got := do(r, "GET", p, "1.2.3.4", nil, "").Header().Get("X-Cache"); got != "MISS" {
			t.Fatalf("data bump kept %s entry: %q", p, got)
		}
	}
}

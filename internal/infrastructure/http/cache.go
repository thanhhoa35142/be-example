package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
	"golang.org/x/sync/singleflight"
)

// CacheOptions configures the GET response cache.
type CacheOptions struct {
	// TTL is how long an entry lives. Versioned entries also die on the
	// next worker data bump; TTL is the fallback when Redis versioning is
	// unavailable.
	TTL time.Duration
	// Versioned mixes cache.VersionKey into the key so worker bumps
	// invalidate instantly. Use for responses derived from precomputed
	// tables (spots, scores, forecasts, recommendations).
	Versioned bool
	// WeatherVersioned additionally mixes cache.WeatherVersionKey. Only
	// routes that return raw weather rows (forecast) or the weather
	// provider (spot detail) need it: weather that matters to every other
	// route reaches it through scores, whose recompute bumps the data
	// version. Implies Versioned.
	WeatherVersioned bool
	// Normalize rewrites a copy of the query values before the key is
	// built. It must canonicalize values AND drop params that must not mint
	// new entries (unknown params, per-request noise); it must not mutate
	// c.Request. Only 200 responses are stored, so canonicalization may
	// merge requests the handler treats identically — never map a request
	// the handler would reject onto a key a valid request can hit.
	Normalize func(v url.Values)
	// Stats receives hit/miss/bypass counters; nil = DefaultCacheStats.
	Stats *CacheStats
}

const (
	cacheTimeout = 300 * time.Millisecond // Redis reads
	// cacheWriteTimeout bounds the store of a computed response. The write
	// is detached from the request (the client may be gone, the handler may
	// have taken longer than cacheTimeout) so it gets its own budget.
	cacheWriteTimeout = 500 * time.Millisecond
	maxCacheEntry     = 1 << 20 // 1 MiB identity body — bigger responses bypass the cache
	cacheKeyPrefix    = "pic:resp:"

	cacheHeaderHit       = "HIT"
	cacheHeaderMiss      = "MISS"
	cacheHeaderBypass    = "BYPASS"
	cacheHeaderCoalesced = "COALESCED"

	// cacheCooldown is how long the middleware bypasses Redis after a
	// failure, so an outage costs one slow request per window instead of a
	// timeout on every request.
	cacheCooldown = 5 * time.Second
)

// cachedEntry is a stored response. gz is the pre-compressed body (nil when
// the body is too small or incompressible).
type cachedEntry struct {
	hdr  map[string][]string
	body []byte
	gz   []byte
}

// Cache memoizes GET responses in Redis. Only 200 + JSON bodies are stored;
// everything else (errors, redirects, personalized routes) passes through.
// Redis failures fail open — the request is served uncached — and a short
// circuit breaker skips Redis entirely while it is down. Client cancellation
// is not a Redis failure and never trips the breaker.
//
// Entries are Redis hashes: "b" = identity body, "z" = the same body gzipped
// (served as is to clients that accept gzip, so hits cost no CPU), "h" =
// JSON object of the response headers (minus per-request ones), so cache
// hits keep headers like Cache-Control.
//
// Concurrent misses for one key are coalesced: one request runs the handler
// and stores the result, the others are answered from that result (or run the
// handler themselves when it was not cacheable). Each request still writes
// only to its own gin.Context.
//
// X-Cache says what happened: HIT (from Redis), MISS (handler ran, response
// cacheable), BYPASS (handler ran, response not cacheable), COALESCED
// (answered from a concurrent request's result). No header is sent when the
// cache is disabled or Redis is bypassed before the handler runs.
func Cache(rdb *redis.Client, opts CacheOptions) gin.HandlerFunc {
	var mu sync.Mutex
	var downUntil time.Time
	down := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Before(downUntil)
	}
	markDown := func(rs *routeStats) {
		mu.Lock()
		downUntil = time.Now().Add(cacheCooldown)
		mu.Unlock()
		rs.breakerOpn.Add(1)
	}
	stats := opts.Stats
	if stats == nil {
		stats = DefaultCacheStats
	}
	var flight singleflight.Group
	versioned := opts.Versioned || opts.WeatherVersioned

	return func(c *gin.Context) {
		rs := stats.route(c)
		switch {
		case rdb == nil:
			rs.bypassed(bypassDisabled)
			c.Next()
			return
		case c.Request.Method != http.MethodGet || c.GetHeader("Authorization") != "":
			rs.bypassed(bypassRequest)
			c.Next()
			return
		case down():
			rs.bypassed(bypassBreaker)
			c.Next()
			return
		}

		reqCtx := c.Request.Context()
		// failOpen serves the request uncached after a Redis error. Only a
		// genuine Redis/network failure (or our own timeout) opens the
		// breaker: when the request's context is done the error is the
		// client's doing — the app cancels in-flight map requests all the
		// time — and says nothing about Redis.
		failOpen := func(err error) {
			if reqCtx.Err() != nil || errors.Is(err, context.Canceled) {
				rs.bypassed(bypassCanceled)
			} else {
				markDown(rs)
				rs.bypassed(bypassRedis)
			}
			c.Next()
		}

		ctx, cancel := context.WithTimeout(reqCtx, cacheTimeout)
		defer cancel()

		// The version is part of the key, so it must be read first. On
		// Redis outage everything fails open.
		version, err := readVersion(ctx, rdb, versioned, opts.WeatherVersioned)
		if err != nil {
			failOpen(err)
			return
		}
		key := cacheKey(c, opts, version)
		gzOK := acceptsGzip(c.Request.Header)

		entry, err := loadEntry(ctx, rdb, key, gzOK)
		if err != nil {
			failOpen(err)
			return
		}
		if entry != nil {
			rs.hit.Add(1)
			serveEntry(c, entry, gzOK, cacheHeaderHit)
			return
		}

		// Miss. One request per key computes; concurrent ones wait for its
		// result instead of all hitting the database. Do (not DoChan): a
		// panic is re-raised on the caller's own goroutine, where gin's
		// Recovery can catch it — and fn recovers it itself so waiters
		// never share it.
		var ran bool
		var panicVal any
		res, _, _ := flight.Do(key, func() (any, error) {
			ran = true
			var e *cachedEntry
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicVal = r
					}
				}()
				e = computeAndStore(c, rdb, key, opts.TTL, rs, markDown)
			}()
			if e == nil {
				return nil, errNotShareable
			}
			return e, nil
		})
		if ran {
			if panicVal != nil {
				panic(panicVal)
			}
			return
		}
		if shared, ok := res.(*cachedEntry); ok && shared != nil {
			rs.coalesced.Add(1)
			serveEntry(c, shared, gzOK, cacheHeaderCoalesced)
			return
		}
		// The leader's response was not cacheable (error, too big, panic):
		// compute our own.
		computeAndStore(c, rdb, key, opts.TTL, rs, markDown)
	}
}

var errNotShareable = errors.New("response not cacheable")

// readVersion returns the data version (plus the weather version when asked)
// that keys are minted under; "" = unversioned / unset.
func readVersion(ctx context.Context, rdb *redis.Client, versioned, weather bool) (string, error) {
	if !versioned {
		return "", nil
	}
	if !weather {
		v, err := rdb.Get(ctx, cache.VersionKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", err
		}
		return v, nil // unset on a fresh Redis: proceed with ""
	}
	vals, err := rdb.MGet(ctx, cache.VersionKey, cache.WeatherVersionKey).Result()
	if err != nil {
		return "", err
	}
	str := func(i int) string {
		if i < len(vals) {
			if s, ok := vals[i].(string); ok {
				return s
			}
		}
		return ""
	}
	return str(0) + "." + str(1), nil
}

// loadEntry fetches a stored response, or nil on a miss. Clients that accept
// gzip read the compressed body ("z") and only fall back to the identity body
// when none was stored; others read "b" directly — so a hit never moves both.
func loadEntry(ctx context.Context, rdb *redis.Client, key string, gz bool) (*cachedEntry, error) {
	field := "b"
	if gz {
		field = "z"
	}
	vals, err := rdb.HMGet(ctx, key, "h", field).Result()
	if err != nil {
		return nil, err
	}
	hraw, _ := vals[0].(string)
	if hraw == "" {
		return nil, nil // miss
	}
	var hdr map[string][]string
	if err := json.Unmarshal([]byte(hraw), &hdr); err != nil {
		return nil, nil // corrupt entry: treat as a miss, it gets overwritten
	}
	e := &cachedEntry{hdr: hdr}
	if s, _ := vals[1].(string); s != "" {
		if gz {
			e.gz = []byte(s)
		} else {
			e.body = []byte(s)
		}
		return e, nil
	}
	if !gz {
		return nil, nil // headers without a body: treat as a miss
	}
	// gzip client, no compressed copy stored (tiny or incompressible body).
	b, err := rdb.HGet(ctx, key, "b").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	e.body = []byte(b)
	return e, nil
}

// serveEntry writes a stored/shared response. A gzip client gets the
// pre-compressed body with Content-Encoding/Content-Length already set —
// the outer Gzip middleware sees the encoding and leaves it alone — and
// everyone else the identity body.
func serveEntry(c *gin.Context, e *cachedEntry, gzOK bool, xcache string) {
	applyCachedHeaders(c, e.hdr)
	h := c.Writer.Header()
	addVary(h, "Accept-Encoding")
	h.Set("X-Cache", xcache)
	body := e.body
	if gzOK && e.gz != nil {
		body = e.gz
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(body)
	c.Abort()
}

// computeAndStore runs the rest of the chain, tees the response, and stores
// it when cacheable. It returns the entry for coalesced waiters, nil when
// the response must not be shared.
func computeAndStore(c *gin.Context, rdb *redis.Client, key string, ttl time.Duration, rs *routeStats, markDown func(*routeStats)) *cachedEntry {
	addVary(c.Writer.Header(), "Accept-Encoding")
	w := &cacheWriter{ResponseWriter: c.Writer}
	c.Writer = w
	c.Next()

	switch {
	case !w.decided:
		rs.bypassed(bypassEmpty)
		return nil
	case w.reason != bypassNone:
		rs.bypassed(w.reason)
		return nil
	case w.overflow:
		rs.bypassed(bypassSize) // grew past the cap on a later write
		return nil
	}
	hdrs := exportHeaders(w.Header())
	hdrJSON, err := json.Marshal(hdrs)
	if err != nil {
		rs.bypassed(bypassEmpty)
		return nil
	}
	rs.miss.Add(1)
	e := &cachedEntry{hdr: hdrs, body: w.buf.Bytes()}
	if len(e.body) >= minGzipBytes {
		if z, ok := gzipBytes(e.body); ok {
			e.gz = z
		}
	}

	// Detached from the request: the handler may have outlived the read
	// timeout and the client may be gone, neither is a reason to drop the
	// entry (or to blame Redis).
	wctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), cacheWriteTimeout)
	defer cancel()
	pipe := rdb.TxPipeline()
	fields := []any{"h", hdrJSON, "b", e.body}
	if e.gz != nil {
		fields = append(fields, "z", e.gz)
	}
	pipe.HSet(wctx, key, fields...)
	pipe.Expire(wctx, key, ttl)
	if _, err := pipe.Exec(wctx); err != nil {
		rs.storeErr.Add(1)
		markDown(rs)
	}
	return e
}

// cacheKey builds "pic:resp:" + sha256 over method, canonical query and the
// current score hour. The hour bucket guarantees correctness at hour
// rollovers even when the client omits `when`/`from`.
func cacheKey(c *gin.Context, opts CacheOptions, version string) string {
	u := c.Request.URL
	q := u.Query()
	if opts.Normalize != nil {
		opts.Normalize(q)
	}
	var sb strings.Builder
	sb.WriteString(c.Request.Method)
	sb.WriteByte(' ')
	sb.WriteString(u.Path)
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			sb.WriteByte('\n')
			sb.WriteString(k)
			sb.WriteByte('=')
			sb.WriteString(v)
		}
	}
	sb.WriteString("\n@")
	sb.WriteString(strconv.FormatInt(time.Now().UTC().Truncate(time.Hour).Unix(), 10))
	if version != "" {
		sb.WriteString("\n#")
		sb.WriteString(version)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return cacheKeyPrefix + hex.EncodeToString(sum[:])
}

// bypassNone is the zero "reason": the response is cacheable.
const bypassNone bypassReason = -1

// cacheWriter tees the response body into buf while passing everything
// through to the client unchanged. At the first byte (or WriteHeaderNow) the
// status, content type and size are known, so that is where the honest
// X-Cache value (MISS = will be stored, BYPASS = will not) is set — headers
// written after the body reaches the client are lost.
type cacheWriter struct {
	gin.ResponseWriter
	buf      bytes.Buffer
	overflow bool
	decided  bool
	reason   bypassReason // bypassNone when cacheable
}

func (w *cacheWriter) decide(first int) {
	if w.decided {
		return
	}
	w.decided = true
	switch {
	case w.ResponseWriter.Status() != http.StatusOK:
		w.reason = bypassStatus
	case !strings.Contains(w.Header().Get("Content-Type"), "application/json"):
		w.reason = bypassContent
	case first > maxCacheEntry:
		w.reason = bypassSize
	default:
		w.reason = bypassNone
	}
	if w.reason == bypassNone {
		w.Header().Set("X-Cache", cacheHeaderMiss)
	} else {
		w.Header().Set("X-Cache", cacheHeaderBypass)
	}
}

func (w *cacheWriter) Write(b []byte) (int, error) {
	w.decide(len(b))
	if w.reason == bypassNone && !w.overflow {
		if w.buf.Len()+len(b) <= maxCacheEntry {
			w.buf.Write(b)
		} else {
			w.overflow = true
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *cacheWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// WriteHeaderNow is how status-only responses (c.AbortWithStatus) reach the
// client; the X-Cache decision must happen before the headers go out.
func (w *cacheWriter) WriteHeaderNow() {
	w.decide(0)
	w.ResponseWriter.WriteHeaderNow()
}

// Per-request headers that must never be replayed from a shared entry:
// X-Cache is set fresh on every response; X-Request-ID/Content-Length belong
// to the individual request; Content-Encoding depends on the client's
// Accept-Encoding (the gzip middleware sits outside the cache); Vary is
// re-added on every response, so which client populated the entry is moot.
var cacheSkipHeaders = map[string]bool{
	"X-Cache":          true,
	"X-Request-Id":     true,
	"Content-Length":   true,
	"Content-Encoding": true,
	"Vary":             true,
}

func exportHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if cacheSkipHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func applyCachedHeaders(c *gin.Context, hdrs map[string][]string) {
	h := c.Writer.Header()
	for k, vs := range hdrs {
		if cacheSkipHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		h[k] = append([]string(nil), vs...)
	}
}

// ---------------------------------------------------------------------------
// Query canonicalization for cache keys
// ---------------------------------------------------------------------------

// keepOnly drops every query param not in allowed so junk params (e.g.
// &x=1, &x=2…) cannot mint new cache entries.
func keepOnly(v url.Values, allowed ...string) {
	set := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		set[k] = struct{}{}
	}
	for k := range v {
		if _, ok := set[k]; !ok {
			delete(v, k)
		}
	}
}

// canon1 rewrites v[key] to the canonical form of its first value. Handlers
// read params via Get (first value wins); canon returns ok=false to leave
// the raw value when it cannot be parsed — the handler will reject the
// request and the response is never cached, so the key is never consulted.
func canon1(v url.Values, key string, canon func(string) (string, bool)) {
	raw := v.Get(key)
	if raw == "" {
		return
	}
	if s, ok := canon(raw); ok {
		v.Set(key, s)
	}
}

func canonFloat(f func(float64) float64) func(string) (string, bool) {
	return func(raw string) (string, bool) {
		x, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", false
		}
		return strconv.FormatFloat(f(x), 'f', -1, 64), true
	}
}

func canonInt(raw string) (string, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return "", false
	}
	return strconv.Itoa(n), true
}

// canonTimeHour truncates an RFC3339 timestamp to the hour — the same
// quantization the services apply, so requests inside one hour share keys.
func canonTimeHour(raw string) (string, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", false
	}
	return t.UTC().Truncate(time.Hour).Format(time.RFC3339), true
}

// canonEnumList sorts and dedupes a comma list, mirroring how the handlers
// parse it (trim + dedupe, order irrelevant). An empty element means the
// request is invalid — left raw so it can never alias a valid key.
func canonEnumList(raw string) (string, bool) {
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return "", false
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ","), true
}

// canonMinScore mirrors ParseFilter: trim, integer 0..100.
func canonMinScore(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 100 {
		return "", false
	}
	return strconv.Itoa(n), true
}

func canonicalizeFilterParams(v url.Values) {
	canon1(v, "kinds", canonEnumList)
	canon1(v, "amenities", canonEnumList)
	canon1(v, "minScore", canonMinScore)
}

func canonIDs(raw string) (string, bool) {
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		id, err := uuid.Parse(strings.TrimSpace(p))
		if err != nil {
			return "", false
		}
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	return strings.Join(ids, ","), true
}

func canonInclude(raw string) (string, bool) {
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue // the handler ignores empty include values
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ","), true
}

func canonBBox(raw string) (string, bool) {
	b, err := parseBBox(raw)
	if err != nil {
		return "", false
	}
	b = spots.CanonicalBBox(b)
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	return strings.Join([]string{f(b.West), f(b.South), f(b.East), f(b.North)}, ","), true
}

// SpotsNormalize canonicalizes GET /v1/spots for the cache key: bbox snapped
// to the same grid the handler queries, ids sorted, timestamps truncated to
// the hour, and every param the handler ignores dropped.
func SpotsNormalize(v url.Values) {
	keepOnly(v, "bbox", "ids", "when", "from", "hours", "include",
		"kinds", "amenities", "minScore")
	canon1(v, "bbox", canonBBox)
	canon1(v, "ids", canonIDs)
	canon1(v, "when", canonTimeHour)
	canon1(v, "from", canonTimeHour)
	canon1(v, "hours", canonInt)
	canon1(v, "include", canonInclude)
	canonicalizeFilterParams(v)
}

// canonGeo rounds a coordinate to ~110 m — the same rounding the handler
// applies before querying, so the cached distanceM belongs to the canonical
// position, not to whichever client populated the entry first.
func canonGeo(limit float64) func(float64) float64 {
	return func(f float64) float64 {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > limit {
			return f // out of range: pass through, the service rejects it
		}
		return round3(f)
	}
}

// canonRadiusM snaps the search radius to the same 100 m bucket the cache
// key uses. Out-of-range values pass through untouched so the service still
// applies its default/reject rules.
func canonRadiusM(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > recommendations.MaxRadiusM {
		return f
	}
	return math.Round(f/100) * 100
}

// RecNormalize canonicalizes GET /v1/recommendations for the cache key.
// lat/lon/radius/when are rounded to the exact values the handler queries
// with, so a cache hit can never leak another user's distances.
func RecNormalize(v url.Values) {
	keepOnly(v, "lat", "lon", "radius", "when", "limit",
		"kinds", "amenities", "minScore")
	canon1(v, "lat", canonFloat(canonGeo(90)))
	canon1(v, "lon", canonFloat(canonGeo(180)))
	canon1(v, "radius", canonFloat(canonRadiusM))
	canon1(v, "when", canonTimeHour)
	canon1(v, "limit", canonInt)
	canonicalizeFilterParams(v)
}

// DropQueryParams removes every query param from the cache key. Use for
// endpoints that take no query parameters at all — any param is noise.
func DropQueryParams(v url.Values) {
	for k := range v {
		delete(v, k)
	}
}

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

type CacheOptions struct {
	TTL time.Duration

	Versioned bool

	WeatherVersioned bool

	Normalize func(v url.Values)

	Stats *CacheStats
}

const (
	cacheTimeout = 300 * time.Millisecond

	cacheWriteTimeout = 500 * time.Millisecond
	maxCacheEntry     = 1 << 20
	cacheKeyPrefix    = "pic:resp:"

	cacheHeaderHit       = "HIT"
	cacheHeaderMiss      = "MISS"
	cacheHeaderBypass    = "BYPASS"
	cacheHeaderCoalesced = "COALESCED"

	cacheCooldown = 5 * time.Second
)

type cachedEntry struct {
	hdr  map[string][]string
	body []byte
	gz   []byte
}

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

		computeAndStore(c, rdb, key, opts.TTL, rs, markDown)
	}
}

var errNotShareable = errors.New("response not cacheable")

func readVersion(ctx context.Context, rdb *redis.Client, versioned, weather bool) (string, error) {
	if !versioned {
		return "", nil
	}
	if !weather {
		v, err := rdb.Get(ctx, cache.VersionKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", err
		}
		return v, nil
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
		return nil, nil
	}
	var hdr map[string][]string
	if err := json.Unmarshal([]byte(hraw), &hdr); err != nil {
		return nil, nil
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
		return nil, nil
	}

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
		rs.bypassed(bypassSize)
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

const bypassNone bypassReason = -1

type cacheWriter struct {
	gin.ResponseWriter
	buf      bytes.Buffer
	overflow bool
	decided  bool
	reason   bypassReason
}

func (w *cacheWriter) decide(first int) {
	if w.decided {
		return
	}
	w.decided = true
	switch {
	case w.Status() != http.StatusOK:
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

func (w *cacheWriter) WriteHeaderNow() {
	w.decide(0)
	w.ResponseWriter.WriteHeaderNow()
}

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

func canonTimeHour(raw string) (string, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", false
	}
	return t.UTC().Truncate(time.Hour).Format(time.RFC3339), true
}

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
			continue
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

func canonGeo(limit float64) func(float64) float64 {
	return func(f float64) float64 {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > limit {
			return f
		}
		return round3(f)
	}
}

func canonRadiusM(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > recommendations.MaxRadiusM {
		return f
	}
	return math.Round(f/100) * 100
}

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

func DropQueryParams(v url.Values) {
	for k := range v {
		delete(v, k)
	}
}

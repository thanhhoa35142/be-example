package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/platform/cache"
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
	// Normalize optionally rewrites a copy of the query values before the
	// key is built (e.g. round lat/lon so nearby requests share entries).
	// It must not mutate c.Request.
	Normalize func(v url.Values)
}

const (
	cacheTimeout    = 300 * time.Millisecond
	maxCacheEntry   = 1 << 20 // 1 MiB — bigger responses bypass the cache
	cacheKeyPrefix  = "pic:resp:"
	cacheHeaderHit  = "HIT"
	cacheHeaderMiss = "MISS"
)

// Cache memoizes GET responses in Redis. Only 200 + JSON bodies are stored;
// everything else (errors, redirects, personalized routes) passes through.
// Redis failures fail open — the request is served uncached.
func Cache(rdb *redis.Client, opts CacheOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rdb == nil || c.Request.Method != http.MethodGet || c.GetHeader("Authorization") != "" {
			c.Next()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), cacheTimeout)
		defer cancel()

		// The version is part of the key, so it must be read first. Two
		// small GETs per request; on Redis outage both fail open.
		version := ""
		if opts.Versioned {
			version, _ = rdb.Get(ctx, cache.VersionKey).Result()
		}
		key := cacheKey(c, opts, version)
		entry, _ := rdb.Get(ctx, key).Result()

		if entry != "" {
			c.Header("X-Cache", cacheHeaderHit)
			c.Data(http.StatusOK, "application/json", []byte(entry))
			c.Abort()
			return
		}

		w := &cacheWriter{ResponseWriter: c.Writer}
		c.Writer = w
		c.Next()

		if w.Status() != http.StatusOK || w.Size() > maxCacheEntry || w.overflow {
			return
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			return
		}
		if err := rdb.Set(ctx, key, w.buf.Bytes(), opts.TTL).Err(); err == nil {
			c.Header("X-Cache", cacheHeaderMiss)
		}
	}
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

// cacheWriter tees the response body into buf while passing everything
// through to the client unchanged.
type cacheWriter struct {
	gin.ResponseWriter
	buf      bytes.Buffer
	overflow bool
}

func (w *cacheWriter) Write(b []byte) (int, error) {
	if w.buf.Len()+len(b) <= maxCacheEntry {
		w.buf.Write(b)
	} else {
		w.overflow = true
	}
	return w.ResponseWriter.Write(b)
}

// RecNormalize rounds lat/lon to ~110 m and buckets radius so nearby
// recommendation requests share cache entries.
func RecNormalize(v url.Values) {
	if raw := v.Get("lat"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.Set("lat", strconv.FormatFloat(round3(f), 'f', 3, 64))
		}
	}
	if raw := v.Get("lon"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.Set("lon", strconv.FormatFloat(round3(f), 'f', 3, 64))
		}
	}
	if raw := v.Get("radius"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.Set("radius", strconv.Itoa(int(f/100+0.5)*100))
		}
	}
	if raw := v.Get("when"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			v.Set("when", t.UTC().Truncate(time.Hour).Format(time.RFC3339))
		}
	}
}



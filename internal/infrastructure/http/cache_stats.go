package http

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// bypassReason says why a request skipped the response cache.
type bypassReason int

const (
	bypassDisabled bypassReason = iota // no Redis client configured
	bypassBreaker                      // circuit breaker open after a Redis failure
	bypassRequest                      // not cacheable by nature: non-GET or Authorization header
	bypassRedis                        // Redis failed on this request (opens the breaker)
	bypassCanceled                     // the client went away while we talked to Redis
	bypassStatus                       // handler answered non-200
	bypassContent                      // 200 but not JSON
	bypassSize                         // body over maxCacheEntry
	bypassEmpty                        // handler wrote no body
	numBypassReasons
)

var bypassNames = [numBypassReasons]string{
	bypassDisabled: "disabled",
	bypassBreaker:  "breaker",
	bypassRequest:  "request",
	bypassRedis:    "redis",
	bypassCanceled: "canceled",
	bypassStatus:   "status",
	bypassContent:  "content",
	bypassSize:     "size",
	bypassEmpty:    "empty",
}

// routeStats are the per-route counters. All fields are atomic: they are
// bumped on the request path and read by the admin endpoint.
type routeStats struct {
	hit        atomic.Uint64 // served from Redis
	miss       atomic.Uint64 // computed by the handler and cacheable
	coalesced  atomic.Uint64 // waited on another in-flight miss for the same key
	storeErr   atomic.Uint64 // cacheable miss whose Redis write failed
	breakerOpn atomic.Uint64 // times the circuit breaker opened
	bypass     [numBypassReasons]atomic.Uint64
}

func (r *routeStats) bypassed(why bypassReason) { r.bypass[why].Add(1) }

// CacheStats collects hit/miss/bypass counters per route pattern. It is
// in-process only: with several API replicas each reports its own traffic.
type CacheStats struct {
	started time.Time
	routes  sync.Map // route pattern -> *routeStats
}

func NewCacheStats() *CacheStats { return &CacheStats{started: time.Now()} }

// DefaultCacheStats is what Cache uses unless CacheOptions.Stats is set, and
// what GET /v1/admin/cache reports.
var DefaultCacheStats = NewCacheStats()

func (s *CacheStats) route(c *gin.Context) *routeStats {
	key := c.FullPath()
	if key == "" {
		key = c.Request.URL.Path
	}
	if v, ok := s.routes.Load(key); ok {
		return v.(*routeStats)
	}
	v, _ := s.routes.LoadOrStore(key, &routeStats{})
	return v.(*routeStats)
}

// RouteCacheStats is one route's counters in the admin response.
type RouteCacheStats struct {
	Route     string `json:"route"`
	Hit       uint64 `json:"hit"`
	Miss      uint64 `json:"miss"`
	Coalesced uint64 `json:"coalesced"`
	Bypass    uint64 `json:"bypass"`
	// BypassByReason only lists reasons that occurred.
	BypassByReason map[string]uint64 `json:"bypassByReason"`
	StoreErrors    uint64            `json:"storeErrors"`
	BreakerTrips   uint64            `json:"breakerTrips"`
	// HitRate = (hit+coalesced) / (hit+coalesced+miss): the share of
	// cacheable requests that did not run the handler. Bypassed requests are
	// excluded; null until a cacheable request was seen.
	HitRate *float64 `json:"hitRate"`
}

// CacheStatsSnapshot is the JSON body of GET /v1/admin/cache.
type CacheStatsSnapshot struct {
	Since  time.Time         `json:"since"`
	Routes []RouteCacheStats `json:"routes"`
}

func (s *CacheStats) Snapshot() CacheStatsSnapshot {
	out := CacheStatsSnapshot{Since: s.started.UTC(), Routes: []RouteCacheStats{}}
	s.routes.Range(func(k, v any) bool {
		r := v.(*routeStats)
		rs := RouteCacheStats{
			Route:          k.(string),
			Hit:            r.hit.Load(),
			Miss:           r.miss.Load(),
			Coalesced:      r.coalesced.Load(),
			StoreErrors:    r.storeErr.Load(),
			BreakerTrips:   r.breakerOpn.Load(),
			BypassByReason: map[string]uint64{},
		}
		for i := range r.bypass {
			if n := r.bypass[i].Load(); n > 0 {
				rs.Bypass += n
				rs.BypassByReason[bypassNames[i]] = n
			}
		}
		if cacheable := rs.Hit + rs.Coalesced + rs.Miss; cacheable > 0 {
			rate := float64(rs.Hit+rs.Coalesced) / float64(cacheable)
			rs.HitRate = &rate
		}
		out.Routes = append(out.Routes, rs)
		return true
	})
	sort.Slice(out.Routes, func(i, j int) bool { return out.Routes[i].Route < out.Routes[j].Route })
	return out
}

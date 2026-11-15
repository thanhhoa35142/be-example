package http

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type bypassReason int

const (
	bypassDisabled bypassReason = iota
	bypassBreaker
	bypassRequest
	bypassRedis
	bypassCanceled
	bypassStatus
	bypassContent
	bypassSize
	bypassEmpty
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

type routeStats struct {
	hit        atomic.Uint64
	miss       atomic.Uint64
	coalesced  atomic.Uint64
	storeErr   atomic.Uint64
	breakerOpn atomic.Uint64
	bypass     [numBypassReasons]atomic.Uint64
}

func (r *routeStats) bypassed(why bypassReason) { r.bypass[why].Add(1) }

type CacheStats struct {
	started time.Time
	routes  sync.Map
}

func NewCacheStats() *CacheStats { return &CacheStats{started: time.Now()} }

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

type RouteCacheStats struct {
	Route     string `json:"route"`
	Hit       uint64 `json:"hit"`
	Miss      uint64 `json:"miss"`
	Coalesced uint64 `json:"coalesced"`
	Bypass    uint64 `json:"bypass"`

	BypassByReason map[string]uint64 `json:"bypassByReason"`
	StoreErrors    uint64            `json:"storeErrors"`
	BreakerTrips   uint64            `json:"breakerTrips"`

	HitRate *float64 `json:"hitRate"`
}

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

package main

import (
	"math"
	"sync"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"golang.org/x/time/rate"
)

// keyLimiter enforces each API key's rate_limit_rps as an in-memory token
// bucket. Sufficient for a single MCP replica; revisit (Redis) if the
// service ever scales horizontally and limits must stay exact.
type keyLimiter struct {
	mu       sync.Mutex
	limiters map[uuid.UUID]*rate.Limiter
}

func newKeyLimiter() *keyLimiter {
	return &keyLimiter{limiters: map[uuid.UUID]*rate.Limiter{}}
}

// retryAfter returns 0 when the request is allowed; otherwise the suggested
// Retry-After delay in whole seconds.
func (kl *keyLimiter) retryAfter(key apikeys.ApiKey) int {
	rps := key.RateLimitRPS
	if rps <= 0 {
		return 0
	}
	want := rate.Limit(rps)
	burst := rps * 2 // short bursts tolerated, sustained rate capped

	kl.mu.Lock()
	l, ok := kl.limiters[key.ID]
	if !ok || l.Limit() != want {
		l = rate.NewLimiter(want, burst)
		kl.limiters[key.ID] = l
	}
	kl.mu.Unlock()

	if l.Allow() {
		return 0
	}
	// A full bucket drains in burst/rps seconds.
	return int(math.Ceil(float64(burst) / float64(rps)))
}

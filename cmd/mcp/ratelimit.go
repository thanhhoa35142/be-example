package main

import (
	"math"
	"sync"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"golang.org/x/time/rate"
)

type keyLimiter struct {
	mu       sync.Mutex
	limiters map[uuid.UUID]*rate.Limiter
}

func newKeyLimiter() *keyLimiter {
	return &keyLimiter{limiters: map[uuid.UUID]*rate.Limiter{}}
}

func (kl *keyLimiter) retryAfter(key apikeys.ApiKey) int {
	rps := key.RateLimitRPS
	if rps <= 0 {
		return 0
	}
	want := rate.Limit(rps)
	burst := rps * 2

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

	return int(math.Ceil(float64(burst) / float64(rps)))
}

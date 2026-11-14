// Package ratelimit provides per-key (client IP) token-bucket limiters.
// Limiter is in-memory (per process); RedisLimiter shares buckets across
// replicas via Redis when REDIS_URL is configured.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	sweepEvery = time.Minute
	// maxKeys bounds memory under a key-flood: past it, the idlest keys go.
	maxKeys = 200_000
)

// Rule is a sustained rate with a burst allowance.
type Rule struct {
	Limit rate.Limit
	Burst int
}

// PerMinute builds a Rule of n requests per minute with the given burst
// (burst < 1 is raised to 1).
func PerMinute(n float64, burst int) Rule {
	if burst < 1 {
		burst = 1
	}
	return Rule{Limit: rate.Limit(n / 60), Burst: burst}
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// Limiter hands out one token bucket per key.
type Limiter struct {
	rule Rule
	now  func() time.Time

	mu        sync.Mutex
	entries   map[string]*entry
	lastSweep time.Time
}

// New returns a Limiter enforcing rule per key. A zero/negative Limit or Burst
// disables limiting (Allow always succeeds).
func New(rule Rule) *Limiter {
	return &Limiter{rule: rule, now: time.Now, entries: map[string]*entry{}}
}

// Allow takes one token for key. When denied, retryAfter is the whole-second
// delay (>= 1) after which a retry can succeed.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	return l.take(key, true)
}

// Deny reports whether key is currently over its limit WITHOUT consuming a
// token. Pair with Hit to rate-limit only failures (e.g. bad credentials).
func (l *Limiter) Deny(key string) (denied bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled() {
		return false, 0
	}
	e, ok := l.entries[key]
	if !ok {
		return false, 0
	}
	now := l.now()
	if e.lim.TokensAt(now) >= 1 {
		return false, 0
	}
	return true, l.wait(e.lim, now)
}

// Hit consumes one token for key (recording a failure).
func (l *Limiter) Hit(key string) { l.take(key, false) }

// Len returns the number of tracked keys (for tests/metrics).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *Limiter) disabled() bool { return l.rule.Limit <= 0 || l.rule.Burst <= 0 }

func (l *Limiter) take(key string, report bool) (bool, time.Duration) {
	if l.disabled() {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	e, ok := l.entries[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(l.rule.Limit, l.rule.Burst)}
		l.entries[key] = e
	}
	e.seen = now
	if e.lim.AllowN(now, 1) {
		return true, 0
	}
	if !report {
		return false, 0
	}
	return false, l.wait(e.lim, now)
}

// wait is the time until one token is available, rounded up to whole seconds.
func (l *Limiter) wait(lim *rate.Limiter, now time.Time) time.Duration {
	tokens := lim.TokensAt(now)
	need := (1 - tokens) / float64(l.rule.Limit)
	secs := math.Ceil(need)
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

// sweep evicts idle keys: a bucket untouched long enough to have refilled
// completely is indistinguishable from a fresh one, so dropping it is free.
// Called with l.mu held.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery && len(l.entries) < maxKeys {
		return
	}
	l.lastSweep = now
	idle := time.Duration(float64(l.rule.Burst)/float64(l.rule.Limit)*float64(time.Second)) + time.Minute
	for k, e := range l.entries {
		if now.Sub(e.seen) > idle {
			delete(l.entries, k)
		}
	}
	if len(l.entries) >= maxKeys { // still flooded with live keys: shed the stalest half
		cutoff := now.Add(-idle / 2)
		for k, e := range l.entries {
			if e.seen.Before(cutoff) || len(l.entries) >= maxKeys {
				delete(l.entries, k)
			}
		}
	}
}

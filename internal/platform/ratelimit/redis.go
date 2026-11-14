package ratelimit

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Checker is the rate-limit port used by HTTP middleware. *Limiter (in
// memory) and *RedisLimiter both satisfy it.
type Checker interface {
	// Allow consumes one token; retryAfter is whole seconds until a retry
	// can succeed (>= 1) when denied.
	Allow(key string) (ok bool, retryAfter time.Duration)
	// Deny reports over-limit WITHOUT consuming (for failed-attempt limits).
	Deny(key string) (denied bool, retryAfter time.Duration)
	// Hit consumes one token if available, ignoring the result.
	Hit(key string)
}

// redisCooldown is how long the limiter bypasses Redis after a failure, so
// an outage costs one slow probe per window instead of a timeout per request.
const redisCooldown = 5 * time.Second

// RedisLimiter enforces the same token-bucket semantics as Limiter but
// shares buckets across processes/replicas via Redis. All operations run
// as one Lua script so check-and-consume is atomic.
//
// Failure policy: while Redis is unreachable the limiter falls back to a
// per-process bucket (local) — limits degrade to per-replica instead of
// disappearing. A short circuit breaker skips Redis during the cooldown so
// requests don't each pay the command timeout.
type RedisLimiter struct {
	rdb     *redis.Client
	rule    Rule
	ns      string
	timeout time.Duration
	local   *Limiter

	mu        sync.Mutex
	downUntil time.Time
}

// NewRedis returns a Checker backed by rdb. namespace separates bucket
// classes ("read", "write", "device", "authfail") so different rules never
// share counters.
func NewRedis(rdb *redis.Client, rule Rule, namespace string) *RedisLimiter {
	return &RedisLimiter{
		rdb: rdb, rule: rule, ns: "rl:" + namespace + ":",
		timeout: 500 * time.Millisecond, local: New(rule),
	}
}

var redisAllowScript = redis.NewScript(`
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local consume = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 't', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = capacity
  ts = now
end
tokens = math.min(capacity, tokens + math.max(0, now - ts) / 1000 * rate)
ts = now

local allowed = 1
local retry = 0
if consume == 1 and tokens >= 1 then
  tokens = tokens - 1
elseif tokens < 1 then
  allowed = 0
  retry = math.max(1, math.ceil((1 - tokens) / rate))
end

redis.call('HSET', key, 't', tokens, 'ts', ts)
redis.call('PEXPIRE', key, tonumber(ARGV[5]))
return {allowed, retry}
`)

func (l *RedisLimiter) disabled() bool { return l.rule.Limit <= 0 || l.rule.Burst <= 0 }

func (l *RedisLimiter) down() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Now().Before(l.downUntil)
}

func (l *RedisLimiter) markDown() {
	l.mu.Lock()
	l.downUntil = time.Now().Add(redisCooldown)
	l.mu.Unlock()
}

// eval runs the Lua bucket script. On any Redis error it marks the breaker
// and returns err so callers can fall back to the in-memory limiter.
func (l *RedisLimiter) eval(ctx context.Context, key string, consume int) (bool, time.Duration, error) {
	if l.disabled() {
		return true, 0, nil
	}
	now := time.Now().UnixMilli()
	// Bucket must outlive a full refill plus idle margin (mirrors the
	// in-memory sweep window).
	ttl := int64(math.Ceil(float64(l.rule.Burst)/float64(l.rule.Limit))) + 60
	res, err := redisAllowScript.Run(ctx, l.rdb,
		[]string{l.ns + key},
		l.rule.Burst, float64(l.rule.Limit), now, consume, ttl*1000,
	).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) != 2 {
		return false, 0, errShortResult
	}
	if res[0] == 1 {
		return true, 0, nil
	}
	return false, time.Duration(res[1]) * time.Second, nil
}

var errShortResult = errors.New("ratelimit: short script result")

func (l *RedisLimiter) Allow(key string) (bool, time.Duration) {
	if l.down() {
		return l.local.Allow(key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	ok, retry, err := l.eval(ctx, key, 1)
	if err != nil {
		l.markDown()
		return l.local.Allow(key)
	}
	return ok, retry
}

func (l *RedisLimiter) Deny(key string) (bool, time.Duration) {
	if l.down() {
		return l.local.Deny(key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	ok, retry, err := l.eval(ctx, key, 0)
	if err != nil {
		l.markDown()
		return l.local.Deny(key)
	}
	return !ok, retry
}

func (l *RedisLimiter) Hit(key string) {
	if l.down() {
		l.local.Hit(key)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	if _, _, err := l.eval(ctx, key, 1); err != nil {
		l.markDown()
		l.local.Hit(key)
	}
}

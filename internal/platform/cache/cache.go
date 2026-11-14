// Package cache holds shared keys for the Redis response cache so the API
// (reader) and worker (writer/invalidator) agree on the same namespace.
package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// VersionKey is bumped (INCR) by the worker after any job that changes data
// behind cached GET responses (ingest, weather, crowd, scoring). The API
// mixes its value into cache keys, giving instant invalidation without
// flushing keys.
const VersionKey = "pic:ver:data"

// Bump increments the data version. Failures are logged by the caller's
// context; a missed bump only means caches stay stale until TTL expiry.
func Bump(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, VersionKey).Err()
}

// Version returns the current data version, "0" when unset or on error —
// callers treat a Redis outage as "no versioning" rather than failing.
func Version(ctx context.Context, rdb *redis.Client) string {
	v, err := rdb.Get(ctx, VersionKey).Result()
	if err != nil {
		return ""
	}
	return v
}

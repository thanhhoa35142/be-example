package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

const VersionKey = "pic:ver:data"

const WeatherVersionKey = "pic:ver:weather"

// AdminAggVersionKey versions the cached admin aggregate responses
// (overview/metrics/events). Bumping it invalidates only those entries —
// the public spot caches keep their own version.
const AdminAggVersionKey = "pic:ver:adminagg"

func Bump(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, VersionKey).Err()
}

func BumpAdminAgg(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, AdminAggVersionKey).Err()
}

func BumpWeather(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, WeatherVersionKey).Err()
}

func Version(ctx context.Context, rdb *redis.Client) string {
	v, err := rdb.Get(ctx, VersionKey).Result()
	if err != nil {
		return ""
	}
	return v
}

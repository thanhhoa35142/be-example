package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

const VersionKey = "pic:ver:data"

const WeatherVersionKey = "pic:ver:weather"

func Bump(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, VersionKey).Err()
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

package jobs

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLockNotAcquired = errors.New("job lock not acquired")

func Acquire(ctx context.Context, pool *pgxpool.Pool, name string) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey(name)).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("advisory lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, ErrLockNotAcquired
	}
	return func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey(name))
		conn.Release()
	}, nil
}

func RunWithLock(ctx context.Context, pool *pgxpool.Pool, name string, fn func(ctx context.Context) error) error {
	release, err := Acquire(ctx, pool, name)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

func lockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("pickhour:" + name))
	return int64(h.Sum64())
}

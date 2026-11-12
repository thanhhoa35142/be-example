// Package jobs runs background jobs guarded by Postgres advisory locks so
// overlapping workers never execute the same job twice.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLockNotAcquired = errors.New("job lock not acquired")

// Acquire takes the session-level advisory lock for name and returns a
// release function. Returns ErrLockNotAcquired when another session holds it.
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

// RunWithLock executes fn while holding a session-level advisory lock derived
// from name. If another session holds the lock, it returns ErrLockNotAcquired
// without running fn.
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

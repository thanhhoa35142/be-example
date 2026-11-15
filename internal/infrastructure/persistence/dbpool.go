package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string, runtimeParams map[string]string, maxConns int32) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db config: %w", err)
	}
	if maxConns > 0 {
		poolCfg.MaxConns = maxConns
	}
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	for k, v := range runtimeParams {
		poolCfg.ConnConfig.RuntimeParams[k] = v
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	return pool, nil
}

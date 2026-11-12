package persistence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
)

type APIKeysRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewAPIKeysRepo(q *Queries, pool *pgxpool.Pool) *APIKeysRepo {
	return &APIKeysRepo{q: q, pool: pool}
}

func mapAPIKey(id uuid.UUID, name, desc, prefix string, rps int32,
	expires, lastUsed, revoked pgtype.Timestamptz, created time.Time) apikeys.ApiKey {
	return apikeys.ApiKey{
		ID:           id,
		Name:         name,
		Description:  desc,
		KeyPrefix:    prefix,
		RateLimitRPS: int(rps),
		ExpiresAt:    tsPtr(expires),
		LastUsedAt:   tsPtr(lastUsed),
		RevokedAt:    tsPtr(revoked),
		CreatedAt:    created.UTC(),
	}
}

func mapGrant(apiKeyID uuid.UUID, effect, resource, action string, created time.Time) apikeys.Grant {
	return apikeys.Grant{
		Effect:    effect,
		Resource:  resource,
		Action:    action,
		CreatedAt: created.UTC(),
	}
}

func (r *APIKeysRepo) CreateKey(ctx context.Context, k apikeys.ApiKey, keyHash []byte, grants []apikeys.Grant) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := r.q.WithTx(tx)
	var expires pgtype.Timestamptz
	if k.ExpiresAt != nil {
		expires = pgtype.Timestamptz{Time: k.ExpiresAt.UTC(), Valid: true}
	}
	row, err := q.InsertAPIKey(ctx, InsertAPIKeyParams{
		Name:         k.Name,
		Description:  k.Description,
		KeyPrefix:    k.KeyPrefix,
		KeyHash:      keyHash,
		RateLimitRps: int32(k.RateLimitRPS),
		ExpiresAt:    expires,
	})
	if err != nil {
		return err
	}
	for _, g := range grants {
		if err := q.InsertAPIKeyGrant(ctx, InsertAPIKeyGrantParams{
			ApiKeyID: row.ID,
			Effect:   g.Effect,
			Resource: g.Resource,
			Action:   g.Action,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *APIKeysRepo) FindByHash(ctx context.Context, keyHash []byte) (apikeys.ApiKey, error) {
	row, err := r.q.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		return apikeys.ApiKey{}, err
	}
	return mapAPIKey(row.ID, row.Name, row.Description, row.KeyPrefix,
		row.RateLimitRps, row.ExpiresAt, row.LastUsedAt, row.RevokedAt, row.CreatedAt), nil
}

func (r *APIKeysRepo) GrantsForKey(ctx context.Context, apiKeyID uuid.UUID) ([]apikeys.Grant, error) {
	rows, err := r.q.ListAPIKeyGrants(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	out := make([]apikeys.Grant, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapGrant(uuid.Nil, row.Effect, row.Resource, row.Action, row.CreatedAt))
	}
	return out, nil
}

func (r *APIKeysRepo) TouchLastUsed(ctx context.Context, apiKeyID uuid.UUID) error {
	return r.q.TouchAPIKeyLastUsed(ctx, apiKeyID)
}

func (r *APIKeysRepo) ListKeys(ctx context.Context) ([]apikeys.ApiKey, error) {
	rows, err := r.q.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apikeys.ApiKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapAPIKey(row.ID, row.Name, row.Description, row.KeyPrefix,
			row.RateLimitRps, row.ExpiresAt, row.LastUsedAt, row.RevokedAt, row.CreatedAt))
	}
	return out, nil
}

func (r *APIKeysRepo) Revoke(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.RevokeAPIKey(ctx, id)
	return n > 0, err
}

func (r *APIKeysRepo) AddGrant(ctx context.Context, apiKeyID uuid.UUID, g apikeys.Grant) error {
	return r.q.InsertAPIKeyGrant(ctx, InsertAPIKeyGrantParams{
		ApiKeyID: apiKeyID,
		Effect:   g.Effect,
		Resource: g.Resource,
		Action:   g.Action,
	})
}

func (r *APIKeysRepo) RemoveGrant(ctx context.Context, apiKeyID uuid.UUID, g apikeys.Grant) (bool, error) {
	n, err := r.q.DeleteAPIKeyGrant(ctx, DeleteAPIKeyGrantParams{
		ApiKeyID: apiKeyID,
		Effect:   g.Effect,
		Resource: g.Resource,
		Action:   g.Action,
	})
	return n > 0, err
}

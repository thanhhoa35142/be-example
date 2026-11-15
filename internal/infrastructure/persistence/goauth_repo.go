package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thanhhoa35142/be-example/internal/modules/goauth"
)

type GoAuthRepo struct {
	q *Queries
}

func NewGoAuthRepo(q *Queries) *GoAuthRepo {
	return &GoAuthRepo{q: q}
}

func (r *GoAuthRepo) Upsert(ctx context.Context, t goauth.Token) error {
	return r.q.UpsertAdminGoogleToken(ctx, UpsertAdminGoogleTokenParams{
		AdminUserID:  t.AdminUserID,
		GoogleSub:    t.GoogleSub,
		GoogleEmail:  t.GoogleEmail,
		RefreshToken: t.RefreshToken,
		Scopes:       t.Scopes,
	})
}

func (r *GoAuthRepo) Get(ctx context.Context, userID uuid.UUID) (*goauth.Token, error) {
	row, err := r.q.GetAdminGoogleToken(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, goauth.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &goauth.Token{
		AdminUserID:  row.AdminUserID,
		GoogleSub:    row.GoogleSub,
		GoogleEmail:  row.GoogleEmail,
		RefreshToken: row.RefreshToken,
		Scopes:       row.Scopes,
		CreatedAt:    row.CreatedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
	}, nil
}

func (r *GoAuthRepo) Delete(ctx context.Context, userID uuid.UUID) error {
	return r.q.DeleteAdminGoogleToken(ctx, userID)
}

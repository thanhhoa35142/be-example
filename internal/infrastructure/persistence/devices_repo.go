package persistence

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"gitlab.com/walkalike-group/pickhour/internal/modules/devices"
)

type DevicesRepo struct {
	q *Queries
}

func NewDevicesRepo(q *Queries) *DevicesRepo {
	return &DevicesRepo{q: q}
}

func (r *DevicesRepo) Upsert(ctx context.Context, in devices.RegisterInput) (uuid.UUID, error) {
	prefs, err := json.Marshal(in.Prefs)
	if err != nil || len(in.Prefs) == 0 {
		prefs = []byte("{}")
	}
	id, err := r.q.UpsertDevice(ctx, UpsertDeviceParams{
		ID:         in.ID,
		ApnsToken:  textParam(in.APNsToken),
		ApnsEnv:    textParam(in.APNsEnv),
		Locale:     textParam(in.Locale),
		Tz:         textParam(in.TZ),
		AppVersion: textParam(in.AppVersion),
		Prefs:      prefs,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, devices.ErrConflict
		}
		return uuid.Nil, err
	}
	return id, nil
}

func (r *DevicesRepo) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := r.q.GetActiveDevice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *DevicesRepo) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.SoftDeleteDevice(ctx, id)
	return n > 0, err
}

func textParam(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

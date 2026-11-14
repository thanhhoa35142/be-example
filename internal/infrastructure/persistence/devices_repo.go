package persistence

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thanhhoa35142/be-example/internal/modules/devices"
)

type DevicesRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

// NewDevicesRepo needs the pool to run the APNs-token takeover in one
// transaction.
func NewDevicesRepo(q *Queries, pool *pgxpool.Pool) *DevicesRepo {
	return &DevicesRepo{q: q, pool: pool}
}

func (r *DevicesRepo) Upsert(ctx context.Context, in devices.RegisterInput) (uuid.UUID, error) {
	prefs, err := json.Marshal(in.Prefs)
	if err != nil || len(in.Prefs) == 0 {
		prefs = []byte("{}")
	}
	params := UpsertDeviceParams{
		ID:         in.ID,
		ApnsToken:  textParam(in.APNsToken),
		ApnsEnv:    textParam(in.APNsEnv),
		Locale:     textParam(in.Locale),
		Tz:         textParam(in.TZ),
		AppVersion: textParam(in.AppVersion),
		Prefs:      prefs,
	}
	// A concurrent registration of the same token by a third device can win
	// the unique index between our release and upsert; one retry resolves it
	// (the retry's release demotes the winner, last writer takes the token).
	for attempt := 0; ; attempt++ {
		id, err := r.upsertTakingOverToken(ctx, params)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if attempt == 0 {
				continue
			}
			return uuid.Nil, devices.ErrConflict
		}
		return id, err
	}
}

// upsertTakingOverToken demotes any other live holder of the APNs token
// (token/env cleared, push_status 'unregistered') and upserts the device, in
// one transaction. A reinstall that lost its Keychain gets a fresh device id
// but the same APNs token; the new install takes the token over instead of
// being locked out with a permanent conflict.
func (r *DevicesRepo) upsertTakingOverToken(ctx context.Context, p UpsertDeviceParams) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	if p.ApnsToken.Valid {
		if err := q.ReleaseAPNsToken(ctx, ReleaseAPNsTokenParams{ApnsToken: p.ApnsToken, ID: p.ID}); err != nil {
			return uuid.Nil, err
		}
	}
	id, err := q.UpsertDevice(ctx, p)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
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

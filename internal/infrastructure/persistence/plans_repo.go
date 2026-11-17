package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
)

type PlansRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewPlansRepo(q *Queries, pool *pgxpool.Pool) *PlansRepo {
	return &PlansRepo{q: q, pool: pool}
}

func int2Param(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

func stateParam(s *plans.State) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: string(*s), Valid: true}
}

func statePtr(t pgtype.Text) *plans.State {
	if !t.Valid {
		return nil
	}
	s := plans.State(t.String)
	return &s
}

func altFromJSON(raw []byte) *plans.Alternative {
	if len(raw) == 0 {
		return nil
	}
	var a plans.Alternative
	if err := json.Unmarshal(raw, &a); err != nil || a.Type == "" {
		return nil
	}
	return &a
}

func (r *PlansRepo) Spot(ctx context.Context, id uuid.UUID) (*plans.SpotRef, error) {
	row, err := r.q.PlanSpot(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, plans.ErrSpotNotFound
	}
	if err != nil {
		return nil, err
	}
	return &plans.SpotRef{ID: row.ID, Name: row.Name.String, Kind: row.Kind, TZ: row.Tz}, nil
}

// InsertLimited counts and inserts under a lock on the device row so two
// concurrent creates cannot both pass the cap.
func (r *PlansRepo) InsertLimited(ctx context.Context, p plans.NewPlan, maxActive int) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	if _, err := q.LockDeviceForPlans(ctx, p.DeviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, plans.ErrDeviceInvalid
		}
		return uuid.Nil, err
	}
	n, err := q.CountActiveOwnedPlans(ctx, p.DeviceID)
	if err != nil {
		return uuid.Nil, err
	}
	if int(n) >= maxActive {
		return uuid.Nil, plans.ErrPlanLimit
	}
	row, err := q.InsertPlan(ctx, InsertPlanParams{
		ShareToken:    p.ShareToken,
		DeviceID:      p.DeviceID,
		SpotID:        p.SpotID,
		StartTs:       p.Start,
		EndTs:         p.End,
		Tz:            p.TZ,
		BaselineScore: int2Param(p.Baseline),
		State:         stateParam(p.State),
		Score:         int2Param(p.Score),
		CapReason:     textParam(p.CapReason),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return uuid.Nil, plans.ErrSpotNotFound
		}
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

func (r *PlansRepo) ListForDevice(ctx context.Context, deviceID uuid.UUID) ([]plans.Plan, error) {
	rows, err := r.q.ListDevicePlans(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]plans.Plan, 0, len(rows))
	for _, row := range rows {
		out = append(out, plans.Plan{
			ID:            row.ID,
			Role:          row.Role,
			ShareToken:    row.ShareToken,
			SpotID:        row.SpotID,
			SpotName:      row.SpotName.String,
			SpotKind:      row.SpotKind,
			Start:         row.StartTs.UTC(),
			End:           row.EndTs.UTC(),
			TZ:            row.Tz,
			Baseline:      int2Ptr(row.BaselineScore),
			State:         statePtr(row.State),
			Score:         int2Ptr(row.Score),
			CapReason:     textPtr(row.CapReason),
			Alternative:   altFromJSON(row.Alternative),
			CanceledAt:    timePtr(row.CanceledAt),
			CreatedAt:     row.CreatedAt.UTC(),
			FollowerCount: int(row.FollowerCount),
		})
	}
	return out, nil
}

func (r *PlansRepo) Cancel(ctx context.Context, id, deviceID uuid.UUID) (bool, error) {
	n, err := r.q.CancelPlan(ctx, CancelPlanParams{ID: id, DeviceID: deviceID})
	return n > 0, err
}

func (r *PlansRepo) ActiveByToken(ctx context.Context, token string) (*plans.Shared, error) {
	row, err := r.q.ActivePlanByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, plans.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sh := &plans.Shared{
		SpotID:   row.SpotID,
		SpotName: row.SpotName.String,
		SpotKind: row.SpotKind,
		SpotCity: row.SpotCity.String,
		Lat:      row.Lat,
		Lon:      row.Lon,
		Start:    row.StartTs.UTC(),
		End:      row.EndTs.UTC(),
		TZ:       row.Tz,
	}
	return sh.WithBaseline(int2Ptr(row.BaselineScore)), nil
}

// Follow locks the plan row so the follower cap cannot be raced; already
// following is a no-op success.
func (r *PlansRepo) Follow(ctx context.Context, token string, deviceID uuid.UUID, maxFollowers, maxFollows int) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	p, err := q.LockActivePlanByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, plans.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if p.DeviceID == deviceID {
		return uuid.Nil, plans.ErrOwnPlan
	}
	exists, err := q.PlanFollowerExists(ctx, PlanFollowerExistsParams{PlanID: p.ID, DeviceID: deviceID})
	if err != nil {
		return uuid.Nil, err
	}
	if exists {
		return p.ID, nil
	}
	n, err := q.CountPlanFollowers(ctx, p.ID)
	if err != nil {
		return uuid.Nil, err
	}
	if int(n) >= maxFollowers {
		return uuid.Nil, plans.ErrFollowerLimit
	}
	// Serialize this device's follows too (two plans followed at once).
	if _, err := q.LockDeviceForPlans(ctx, deviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, plans.ErrDeviceInvalid
		}
		return uuid.Nil, err
	}
	f, err := q.CountActiveFollows(ctx, deviceID)
	if err != nil {
		return uuid.Nil, err
	}
	if int(f) >= maxFollows {
		return uuid.Nil, plans.ErrFollowLimit
	}
	if err := q.InsertPlanFollower(ctx, InsertPlanFollowerParams{PlanID: p.ID, DeviceID: deviceID}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return p.ID, nil
}

func (r *PlansRepo) Unfollow(ctx context.Context, token string, deviceID uuid.UUID) error {
	return r.q.DeletePlanFollowByToken(ctx, DeletePlanFollowByTokenParams{ShareToken: token, DeviceID: deviceID})
}

func (r *PlansRepo) WindowScores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]plans.HourScore, error) {
	rows, err := r.q.PlanWindowScores(ctx, PlanWindowScoresParams{SpotID: spotID, FromHour: from, ToTs: to})
	if err != nil {
		return nil, err
	}
	out := make([]plans.HourScore, 0, len(rows))
	for _, row := range rows {
		out = append(out, plans.HourScore{
			TS:        row.TsHour.UTC(),
			Score:     int(row.Score),
			CapReason: textPtr(row.CapReason),
			Reasons:   reasonsFromJSON(row.Reasons),
		})
	}
	return out, nil
}

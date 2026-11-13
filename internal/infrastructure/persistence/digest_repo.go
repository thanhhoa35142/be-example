package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/digest"
)

type DigestRepo struct {
	q *Queries
}

func NewDigestRepo(q *Queries) *DigestRepo { return &DigestRepo{q: q} }

func dateParam(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	y, m, d := t.Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func datePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func digestFrom(id uuid.UUID, spots []uuid.UUID, enabled bool, dow, hour int16, last pgtype.Date) *digest.Digest {
	return &digest.Digest{
		DeviceID: id, SpotIDs: spots, Enabled: enabled,
		SendDow: int(dow), SendHour: int(hour), LastSentOn: datePtr(last),
	}
}

func (r *DigestRepo) Upsert(ctx context.Context, deviceID uuid.UUID, spotIDs []uuid.UUID, enabled bool) (*digest.Digest, error) {
	row, err := r.q.UpsertWeeklyDigest(ctx, UpsertWeeklyDigestParams{DeviceID: deviceID, SpotIds: spotIDs, Enabled: enabled})
	if err != nil {
		return nil, err
	}
	return digestFrom(row.DeviceID, row.SpotIds, row.Enabled, row.SendDow, row.SendHour, row.LastSentOn), nil
}

func (r *DigestRepo) Get(ctx context.Context, deviceID uuid.UUID) (*digest.Digest, error) {
	row, err := r.q.GetWeeklyDigest(ctx, deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, digest.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return digestFrom(row.DeviceID, row.SpotIds, row.Enabled, row.SendDow, row.SendHour, row.LastSentOn), nil
}

func (r *DigestRepo) Delete(ctx context.Context, deviceID uuid.UUID) error {
	_, err := r.q.DeleteWeeklyDigest(ctx, deviceID)
	return err
}

func (r *DigestRepo) DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	return r.q.DeviceExistsForAlert(ctx, deviceID)
}

func (r *DigestRepo) ResolveSpots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := r.q.ResolveDigestSpotIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]uuid.UUID, len(rows))
	for _, row := range rows {
		out[row.Requested] = row.Resolved
	}
	return out, nil
}

func (r *DigestRepo) ListCandidates(ctx context.Context) ([]digest.Candidate, error) {
	rows, err := r.q.ListDigestCandidates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]digest.Candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, digest.Candidate{
			Digest:   *digestFrom(row.DeviceID, row.SpotIds, row.Enabled, row.SendDow, row.SendHour, row.LastSentOn),
			DeviceTZ: row.DeviceTz,
		})
	}
	return out, nil
}

func (r *DigestRepo) Claim(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) (bool, error) {
	_, err := r.q.ClaimWeeklyDigest(ctx, ClaimWeeklyDigestParams{
		LocalDate: dateParam(&localDate), DeviceID: deviceID, Prev: dateParam(prev),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *DigestRepo) Release(ctx context.Context, deviceID uuid.UUID, localDate time.Time, prev *time.Time) error {
	return r.q.ReleaseWeeklyDigest(ctx, ReleaseWeeklyDigestParams{
		Prev: dateParam(prev), DeviceID: deviceID, LocalDate: dateParam(&localDate),
	})
}

func (r *DigestRepo) Spots(ctx context.Context, ids []uuid.UUID) ([]digest.SpotInfo, error) {
	rows, err := r.q.ListDigestSpots(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]digest.SpotInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, digest.SpotInfo{ID: row.ID, Name: row.Name.String, TZ: row.Tz})
	}
	return out, nil
}

func (r *DigestRepo) Scores(ctx context.Context, spotID uuid.UUID, from, to time.Time) ([]digest.HourScore, error) {
	rows, err := r.q.ListDigestScores(ctx, ListDigestScoresParams{SpotID: spotID, TsHour: from, TsHour_2: to})
	if err != nil {
		return nil, err
	}
	out := make([]digest.HourScore, 0, len(rows))
	for _, row := range rows {
		out = append(out, digest.HourScore{Start: row.TsHour, Score: int(row.Score)})
	}
	return out, nil
}

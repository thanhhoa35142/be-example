package persistence

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/geo"
	"github.com/thanhhoa35142/be-example/internal/modules/nudge"
)

type NudgeRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewNudgeRepo(q *Queries, pool *pgxpool.Pool) *NudgeRepo { return &NudgeRepo{q: q, pool: pool} }

func nudgeSub(id uuid.UUID, enabled bool, cell pgtype.Int8, source pgtype.Text, updated pgtype.Timestamptz, hour int16, consent string) *nudge.Subscription {
	var c *int64
	if cell.Valid {
		v := cell.Int64
		c = &v
	}
	var at *time.Time
	if updated.Valid {
		v := updated.Time
		at = &v
	}
	return &nudge.Subscription{
		DeviceID: id, Enabled: enabled, HomeCell: c, HomeSource: source.String,
		HomeUpdatedAt: at, SendHour: int(hour), Consent: consent,
	}
}

func (r *NudgeRepo) Upsert(ctx context.Context, in nudge.SubscriptionUpsert) (*nudge.Subscription, error) {
	var cell pgtype.Int8
	if in.HomeCell != nil {
		cell = pgtype.Int8{Int64: *in.HomeCell, Valid: true}
	}
	row, err := r.q.UpsertNudgeSubscription(ctx, UpsertNudgeSubscriptionParams{
		DeviceID: in.DeviceID, Enabled: in.Enabled, HomeCell: cell, HomeSource: pgtype.Text{String: in.HomeSource, Valid: in.HomeCell != nil && in.HomeSource != ""},
		SendHour: int16(in.SendHour), Consent: in.Consent,
	})
	if err != nil {
		return nil, err
	}
	return nudgeSub(row.DeviceID, row.Enabled, row.HomeCell, row.HomeSource, row.HomeUpdatedAt, row.SendHour, row.Consent), nil
}

func (r *NudgeRepo) Get(ctx context.Context, deviceID uuid.UUID) (*nudge.Subscription, error) {
	row, err := r.q.GetNudgeSubscription(ctx, deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nudge.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return nudgeSub(row.DeviceID, row.Enabled, row.HomeCell, row.HomeSource, row.HomeUpdatedAt, row.SendHour, row.Consent), nil
}

func (r *NudgeRepo) Delete(ctx context.Context, deviceID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	if _, err := q.DeleteNudgeSubscription(ctx, deviceID); err != nil {
		return err
	}
	if err := q.DeleteNudgeDeliveries(ctx, deviceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *NudgeRepo) DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	return r.q.DeviceExistsForAlert(ctx, deviceID)
}

func (r *NudgeRepo) Sweep(ctx context.Context, now time.Time) error {
	if err := r.q.SweepStaleNudgeClaims(ctx, now); err != nil {
		return err
	}
	return r.q.DeadLetterNudgeDeliveries(ctx, int16(nudge.MaxDeliveryAttempts))
}

func (r *NudgeRepo) Candidates(ctx context.Context, now time.Time) ([]nudge.Candidate, error) {
	rows, err := r.q.ListNudgeCandidates(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]nudge.Candidate, 0, len(rows))
	for _, row := range rows {
		c := nudge.Candidate{
			DeviceID:             row.DeviceID,
			TZ:                   row.DeviceTz,
			HomeCell:             row.HomeCell.Int64,
			SendHour:             int(row.SendHour),
			LastSentAt:           timePtr(row.LastSentAt),
			IgnoredStreak:        int(row.IgnoredStreak),
			DigestEnabled:        row.DigestEnabled,
			WeekendNudgeDate:     datePtr(row.WeekendNudgeDate),
			LastDeliveryDate:     datePtr(row.LastDeliveryDate),
			LastDeliveryStatus:   row.LastDeliveryStatus,
			LastDeliveryAttempts: int(row.LastDeliveryAttempts),
		}
		if row.DigestSentAt.Unix() > 0 { // query returns 'epoch' for "none"
			t := row.DigestSentAt
			c.DigestSentAt = &t
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *NudgeRepo) ScoresNear(ctx context.Context, cell int64, radiusM int, from, to time.Time) ([]nudge.SpotHour, error) {
	lat, lng, err := geo.CellCenter(cell)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListNudgeScoresNear(ctx, ListNudgeScoresNearParams{
		FromTs: from, ToTs: to, Lon: lng, Lat: lat, RadiusM: float64(radiusM),
	})
	if err != nil {
		return nil, err
	}
	out := make([]nudge.SpotHour, 0, len(rows))
	for _, row := range rows {
		out = append(out, nudge.SpotHour{
			SpotID: row.SpotID, Name: row.Name, TZ: row.Tz, Quality: int(row.Quality),
			DistanceM: row.DistanceM, Start: row.TsHour, Score: int(row.Score),
		})
	}
	return out, nil
}

func (r *NudgeRepo) Claim(ctx context.Context, d nudge.Delivery, now time.Time) (bool, error) {
	_, err := r.q.ClaimNudgeDelivery(ctx, ClaimNudgeDeliveryParams{
		DeviceID: d.DeviceID, LocalDate: dateParam(&d.LocalDate), Slot: d.Slot,
		TargetDate: dateParam(&d.TargetDate), SpotID: d.SpotID,
		WindowStart: d.WindowStart, WindowEnd: d.WindowEnd, Score: int16(d.Score), Now: now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *NudgeRepo) Reclaim(ctx context.Context, d nudge.Delivery, now time.Time) (bool, error) {
	_, err := r.q.ReclaimFailedNudgeDelivery(ctx, ReclaimFailedNudgeDeliveryParams{
		Now: now, Slot: d.Slot, TargetDate: dateParam(&d.TargetDate), SpotID: d.SpotID,
		WindowStart: d.WindowStart, WindowEnd: d.WindowEnd, Score: int16(d.Score),
		DeviceID: d.DeviceID, LocalDate: dateParam(&d.LocalDate), MaxAttempts: int16(nudge.MaxDeliveryAttempts),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *NudgeRepo) MarkSent(ctx context.Context, d nudge.Delivery, sentAt time.Time, ignoredStreak int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	ts := pgtype.Timestamptz{Time: sentAt, Valid: true}
	if err := q.MarkNudgeDeliverySent(ctx, MarkNudgeDeliverySentParams{
		SentAt: ts, ApnsStatus: pgtype.Int4{Int32: http.StatusOK, Valid: true},
		DeviceID: d.DeviceID, LocalDate: dateParam(&d.LocalDate),
	}); err != nil {
		return err
	}
	if err := q.SetNudgeSent(ctx, SetNudgeSentParams{SentAt: ts, IgnoredStreak: int16(ignoredStreak), DeviceID: d.DeviceID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *NudgeRepo) MarkFailed(ctx context.Context, d nudge.Delivery, reason string) error {
	return r.q.MarkNudgeDeliveryFailed(ctx, MarkNudgeDeliveryFailedParams{
		LastError: pgtype.Text{String: reason, Valid: true},
		DeviceID:  d.DeviceID, LocalDate: dateParam(&d.LocalDate),
	})
}

func (r *NudgeRepo) MarkDead(ctx context.Context, d nudge.Delivery, reason string) error {
	return r.q.MarkNudgeDeliveryDead(ctx, MarkNudgeDeliveryDeadParams{
		LastError: pgtype.Text{String: reason, Valid: true},
		DeviceID:  d.DeviceID, LocalDate: dateParam(&d.LocalDate),
	})
}

func (r *NudgeRepo) RecordPushSend(ctx context.Context, deviceID uuid.UUID, kind string, sentAt time.Time) error {
	return r.q.InsertPushSend(ctx, InsertPushSendParams{DeviceID: deviceID, Kind: kind, SentAt: sentAt})
}

// RecordPushSend lets the alert and digest senders note a delivered push in
// the cross-kind frequency-cap ledger (notifier.PushRecorder).
func (r *AlertsRepo) RecordPushSend(ctx context.Context, deviceID uuid.UUID, kind string, sentAt time.Time) error {
	return r.q.InsertPushSend(ctx, InsertPushSendParams{DeviceID: deviceID, Kind: kind, SentAt: sentAt})
}

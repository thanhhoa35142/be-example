package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
)

type AlertsRepo struct {
	q *Queries
}

func NewAlertsRepo(q *Queries) *AlertsRepo {
	return &AlertsRepo{q: q}
}

func ruleFromRow(r TAlertRule) alerts.Rule {
	var start, end time.Duration
	if r.StartLocal.Valid {
		start = time.Duration(r.StartLocal.Microseconds) * time.Microsecond
	}
	if r.EndLocal.Valid {
		end = time.Duration(r.EndLocal.Microseconds) * time.Microsecond
	}
	var h3 *uint64
	if r.H3Cell.Valid {
		v := uint64(r.H3Cell.Int64)
		h3 = &v
	}
	return alerts.Rule{
		ID:         r.ID,
		DeviceID:   r.DeviceID,
		SpotID:     uuidPtr(r.SpotID),
		H3Cell:     h3,
		RadiusM:    intPtr(r.RadiusM),
		MinScore:   int(r.MinScore),
		DaysMask:   int(r.DaysMask),
		StartLocal: start,
		EndLocal:   end,
		TZ:         r.Tz,
		Enabled:    r.Enabled,
	}
}

func ruleParams(deviceID uuid.UUID, in alerts.RuleInput, start, end time.Duration) InsertAlertRuleParams {
	return InsertAlertRuleParams{
		DeviceID:   deviceID,
		SpotID:     uuidParam(in.SpotID),
		H3Cell:     int8Param(in.H3Cell),
		RadiusM:    int4Param(in.RadiusM),
		MinScore:   int16(in.MinScore),
		DaysMask:   int16(in.DaysMask),
		StartLocal: pgtype.Time{Microseconds: start.Microseconds(), Valid: true},
		EndLocal:   pgtype.Time{Microseconds: end.Microseconds(), Valid: true},
		Tz:         in.TZ,
	}
}

func (r *AlertsRepo) ListRules(ctx context.Context, deviceID uuid.UUID) ([]alerts.Rule, error) {
	rows, err := r.q.ListAlertRules(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]alerts.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, ruleFromRow(row))
	}
	return out, nil
}

func (r *AlertsRepo) GetRule(ctx context.Context, id uuid.UUID) (*alerts.Rule, error) {
	row, err := r.q.GetAlertRule(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, alerts.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rule := ruleFromRow(row)
	return &rule, nil
}

func (r *AlertsRepo) CountRules(ctx context.Context, deviceID uuid.UUID) (int, error) {
	n, err := r.q.CountAlertRules(ctx, deviceID)
	return int(n), err
}

func (r *AlertsRepo) InsertRule(ctx context.Context, deviceID uuid.UUID, in alerts.RuleInput, start, end time.Duration) (uuid.UUID, error) {
	return r.q.InsertAlertRule(ctx, ruleParams(deviceID, in, start, end))
}

func (r *AlertsRepo) UpdateRule(ctx context.Context, id, deviceID uuid.UUID, in alerts.RuleInput, start, end time.Duration) (bool, error) {
	p := ruleParams(deviceID, in, start, end)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	_, err := r.q.UpdateAlertRule(ctx, UpdateAlertRuleParams{
		ID:         id,
		DeviceID:   deviceID,
		SpotID:     p.SpotID,
		H3Cell:     p.H3Cell,
		RadiusM:    p.RadiusM,
		MinScore:   p.MinScore,
		DaysMask:   p.DaysMask,
		StartLocal: p.StartLocal,
		EndLocal:   p.EndLocal,
		Tz:         p.Tz,
		Enabled:    enabled,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *AlertsRepo) DeleteRule(ctx context.Context, id, deviceID uuid.UUID) (bool, error) {
	n, err := r.q.DeleteAlertRule(ctx, DeleteAlertRuleParams{ID: id, DeviceID: deviceID})
	return n > 0, err
}

func (r *AlertsRepo) ListEnabledRules(ctx context.Context) ([]alerts.Rule, error) {
	rows, err := r.q.ListEnabledAlertRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]alerts.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, ruleFromRow(row))
	}
	return out, nil
}

func (r *AlertsRepo) DeviceAPNsToken(ctx context.Context, deviceID uuid.UUID) (string, string, string, bool, error) {
	row, err := r.q.DeviceAPNsToken(ctx, deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return row.ApnsToken.String, row.ApnsEnv, row.Locale.String, row.ApnsToken.Valid, nil
}

func (r *AlertsRepo) SpotScoreAt(ctx context.Context, spotID uuid.UUID, tsHour time.Time) (*alerts.ScoredSpot, error) {
	row, err := r.q.SpotScoreAt(ctx, SpotScoreAtParams{ID: spotID, TsHour: tsHour})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, alerts.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &alerts.ScoredSpot{
		ID:    row.ID,
		Name:  row.Name.String,
		Score: int(row.Score),
	}, nil
}

func (r *AlertsRepo) BestScoredSpotNear(ctx context.Context, lat, lon float64, radiusM int, tsHour time.Time) (*alerts.Delivery, error) {
	row, err := r.q.BestScoredSpotNear(ctx, BestScoredSpotNearParams{
		StMakepoint:   lat,
		StMakepoint_2: lon,
		TsHour:        tsHour,
		StDwithin:     radiusM,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &alerts.Delivery{
		SpotID:   row.ID,
		SpotName: row.Name.String,
		Score:    int(row.Score),
	}, nil
}

func (r *AlertsRepo) SweepDeliveries(ctx context.Context) error {
	if err := r.q.SweepStaleDeliveries(ctx); err != nil {
		return err
	}
	err := r.q.DeadLetterFailedDeliveries(ctx, int16(alerts.MaxDeliveryAttempts))
	return err
}

func (r *AlertsRepo) ClaimDelivery(ctx context.Context, d alerts.Delivery, windowStart time.Time, maxAttempts int) (bool, error) {
	_, err := r.q.ClaimAlertDelivery(ctx, ClaimAlertDeliveryParams{
		RuleID:      d.RuleID,
		WindowStart: windowStart,
	})
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	// Slot exists — only a 'failed' row with attempts left can be re-claimed.
	_, err = r.q.ReclaimFailedDelivery(ctx, ReclaimFailedDeliveryParams{
		RuleID:      d.RuleID,
		WindowStart: windowStart,
		Attempts:    int16(maxAttempts),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *AlertsRepo) MarkDeliverySent(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int) error {
	return r.q.MarkDeliverySent(ctx, MarkDeliverySentParams{
		RuleID:      ruleID,
		WindowStart: windowStart,
		ApnsStatus:  pgtype.Int4{Int32: int32(apnsStatus), Valid: true},
	})
}

func (r *AlertsRepo) MarkDeliveryFailed(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, apnsStatus int, lastError string) error {
	return r.q.MarkDeliveryFailed(ctx, MarkDeliveryFailedParams{
		RuleID:      ruleID,
		WindowStart: windowStart,
		ApnsStatus:  pgtype.Int4{Int32: int32(apnsStatus), Valid: true},
		LastError:   pgtype.Text{String: lastError, Valid: lastError != ""},
	})
}

func (r *AlertsRepo) MarkDeliveryDead(ctx context.Context, ruleID uuid.UUID, windowStart time.Time, lastError string) error {
	return r.q.MarkDeliveryDead(ctx, MarkDeliveryDeadParams{
		RuleID:      ruleID,
		WindowStart: windowStart,
		LastError:   pgtype.Text{String: lastError, Valid: lastError != ""},
	})
}

func (r *AlertsRepo) MarkDeviceUnregistered(ctx context.Context, deviceID uuid.UUID) error {
	return r.q.MarkDevicePushUnregistered(ctx, deviceID)
}

func (r *AlertsRepo) DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	return r.q.DeviceExistsForAlert(ctx, deviceID)
}

// small param helpers local to this file
func uuidParam(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

func int8Param(v *uint64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: int64(*v), Valid: true}
}

func int4Param(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
)

// plans.EvalRepository on PlansRepo (job plans.evaluate).

func civilDateParam(t time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

func (r *PlansRepo) EvaluablePlans(ctx context.Context, until time.Time) ([]plans.EvalPlan, error) {
	rows, err := r.q.EvaluablePlans(ctx, until)
	if err != nil {
		return nil, err
	}
	out := make([]plans.EvalPlan, 0, len(rows))
	for _, row := range rows {
		p := plans.EvalPlan{
			ID: row.ID, ShareToken: row.ShareToken, SpotID: row.SpotID,
			SpotName: row.SpotName.String, SpotKind: row.SpotKind,
			Start: row.StartTs.UTC(), End: row.EndTs.UTC(), TZ: row.Tz,
			Notified:  statePtr(row.NotifiedState),
			Confirmed: row.ConfirmedAt.Valid,
		}
		if row.BaselineScore.Valid {
			b := int(row.BaselineScore.Int16)
			p.Baseline = &b
		}
		if row.LastNotifiedOn.Valid {
			d := row.LastNotifiedOn.Time
			d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
			p.LastNotifiedOn = &d
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *PlansRepo) NearbyAlternative(ctx context.Context, spotID uuid.UUID, from, to time.Time, hours, minMean int, cfg plans.Config) (*plans.Alternative, error) {
	row, err := r.q.NearbyPlanAlternative(ctx, NearbyPlanAlternativeParams{
		ID: spotID, RadiusM: float64(cfg.AltRadiusM), FromHour: from, ToTs: to,
		Hours: int32(hours), BadCaps: cfg.BadCaps, MinMean: int32(minMean),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := row.ID
	return &plans.Alternative{
		Type: plans.AltSpot, SpotID: &id, SpotName: row.Name.String, SpotKind: row.Kind, Score: int(row.Mean),
	}, nil
}

func (r *PlansRepo) SaveEvaluation(ctx context.Context, id uuid.UUID, state plans.State, w plans.Window, alt *plans.Alternative) error {
	var raw []byte
	if alt != nil {
		b, err := json.Marshal(alt)
		if err != nil {
			return err
		}
		raw = b
	}
	score := w.Mean
	return r.q.SavePlanEvaluation(ctx, SavePlanEvaluationParams{
		ID: id, State: stateParam(&state), Score: int2Param(&score),
		CapReason: textParam(w.Cap), Alternative: raw,
	})
}

// ClaimNotification: the guarded plan update and the outbox inserts commit
// together, so a crash cannot advance the state without queueing (or queue
// twice).
func (r *PlansRepo) ClaimNotification(ctx context.Context, n plans.Notification, localDate time.Time) (bool, int64, error) {
	payload, err := json.Marshal(n)
	if err != nil {
		return false, 0, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	date := civilDateParam(localDate)
	switch n.Kind {
	case plans.KindChanged:
		_, err = q.ClaimPlanChange(ctx, ClaimPlanChangeParams{ID: n.PlanID, State: stateParam(&n.State), LocalDate: date})
	case plans.KindConfirmed:
		_, err = q.ClaimPlanConfirm(ctx, ClaimPlanConfirmParams{ID: n.PlanID, LocalDate: date})
	default:
		return false, 0, errors.New("plans: unknown notification kind " + n.Kind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	queued, err := q.EnqueuePlanDeliveries(ctx, EnqueuePlanDeliveriesParams{
		ID: n.PlanID, LocalDate: date, Kind: n.Kind, Payload: payload,
	})
	if err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, queued, nil
}

func (r *PlansRepo) PrepareDeliveries(ctx context.Context, maxAttempts int) error {
	if err := r.q.SweepStalePlanDeliveries(ctx); err != nil {
		return err
	}
	return r.q.ExpirePlanDeliveries(ctx, int32(maxAttempts))
}

func (r *PlansRepo) ClaimDeliveries(ctx context.Context, maxAttempts, batch int) ([]plans.Delivery, error) {
	rows, err := r.q.ClaimPlanDeliveries(ctx, ClaimPlanDeliveriesParams{MaxAttempts: int32(maxAttempts), Batch: int32(batch)})
	if err != nil {
		return nil, err
	}
	out := make([]plans.Delivery, 0, len(rows))
	for _, row := range rows {
		d := plans.Delivery{PlanID: row.PlanID, DeviceID: row.DeviceID, LocalDate: row.LocalDate.Time}
		if err := json.Unmarshal(row.Payload, &d.Notification); err != nil {
			// Unreadable payload: never sendable, give up on the row.
			msg := "payload: " + err.Error()
			_ = r.q.MarkPlanDeliveryFailed(ctx, MarkPlanDeliveryFailedParams{
				PlanID: row.PlanID, DeviceID: row.DeviceID, LocalDate: row.LocalDate,
				Status: "dead", LastError: textParam(&msg),
			})
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *PlansRepo) MarkDeliverySent(ctx context.Context, d plans.Delivery) error {
	return r.q.MarkPlanDeliverySent(ctx, MarkPlanDeliverySentParams{
		PlanID: d.PlanID, DeviceID: d.DeviceID, LocalDate: civilDateParam(d.LocalDate),
	})
}

func (r *PlansRepo) ReleaseDelivery(ctx context.Context, d plans.Delivery) error {
	return r.q.ReleasePlanDelivery(ctx, ReleasePlanDeliveryParams{
		PlanID: d.PlanID, DeviceID: d.DeviceID, LocalDate: civilDateParam(d.LocalDate),
	})
}

func (r *PlansRepo) MarkDeliveryFailed(ctx context.Context, d plans.Delivery, status string, apnsStatus *int, msg string) error {
	return r.q.MarkPlanDeliveryFailed(ctx, MarkPlanDeliveryFailedParams{
		PlanID: d.PlanID, DeviceID: d.DeviceID, LocalDate: civilDateParam(d.LocalDate),
		Status: status, ApnsStatus: int2Param(apnsStatus), LastError: textParam(&msg),
	})
}

var _ plans.EvalRepository = (*PlansRepo)(nil)

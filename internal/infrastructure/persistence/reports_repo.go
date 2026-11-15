package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/thanhhoa35142/be-example/internal/modules/reports"
)

type ReportsRepo struct {
	q *Queries
}

func NewReportsRepo(q *Queries) *ReportsRepo {
	return &ReportsRepo{q: q}
}

func (r *ReportsRepo) DeviceActive(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	_, err := r.q.GetActiveDevice(ctx, deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *ReportsRepo) DeviceTZ(ctx context.Context, deviceID uuid.UUID) (string, error) {
	return r.q.GetDeviceTz(ctx, deviceID)
}

func (r *ReportsRepo) SpotActive(ctx context.Context, spotID uuid.UUID) (bool, error) {
	row, err := r.q.GetSpotByID(ctx, spotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Status == "active", nil
}

func (r *ReportsRepo) InsertSpotReport(ctx context.Context, in reports.SpotReportInput) error {
	var issue pgtype.Text
	if in.IssueCode != nil {
		issue = pgtype.Text{String: *in.IssueCode, Valid: true}
	}
	return r.q.InsertSpotReport(ctx, InsertSpotReportParams{
		SpotID: in.SpotID, DeviceID: in.DeviceID, Type: in.Type, IssueCode: issue,
	})
}

func (r *ReportsRepo) SpotReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error) {
	n, err := r.q.CountSpotReportsSince(ctx, CountSpotReportsSinceParams{DeviceID: deviceID, CreatedAt: since})
	return int(n), err
}

func (r *ReportsRepo) ScoreFeedbackSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error) {
	n, err := r.q.CountScoreFeedbackSince(ctx, CountScoreFeedbackSinceParams{DeviceID: deviceID, CreatedAt: since})
	return int(n), err
}

func (r *ReportsRepo) CrowdReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error) {
	n, err := r.q.CountCrowdReportsSince(ctx, CountCrowdReportsSinceParams{
		DeviceID: deviceID, CreatedAt: since,
	})
	return int(n), err
}

func (r *ReportsRepo) InsertCrowdReport(ctx context.Context, spotID, deviceID uuid.UUID, level, localDow, localHour int) error {
	return r.q.InsertCrowdReport(ctx, InsertCrowdReportParams{
		SpotID: spotID, DeviceID: deviceID,
		Level: int16(level), LocalDow: int16(localDow), LocalHour: int16(localHour),
	})
}

func (r *ReportsRepo) InsertScoreFeedback(ctx context.Context, in reports.ScoreFeedbackInput) error {
	return r.q.InsertScoreFeedback(ctx, InsertScoreFeedbackParams{
		SpotID: in.SpotID, DeviceID: in.DeviceID,
		TsHour: in.TSHour.UTC().Truncate(time.Hour), Verdict: int16(in.Verdict),
	})
}

package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
)

type AdminRepo struct {
	q *Queries
}

func NewAdminRepo(q *Queries) *AdminRepo {
	return &AdminRepo{q: q}
}

// ---- job_run ----

func (r *AdminRepo) StartJobRun(ctx context.Context, job string) (int64, error) {
	return r.q.StartJobRun(ctx, job)
}

func (r *AdminRepo) AbandonJobRuns(ctx context.Context, job string) error {
	return r.q.AbandonJobRuns(ctx, job)
}

func (r *AdminRepo) FinishJobRun(ctx context.Context, id int64, status string, rowsAffected *int, errText *string, stats []byte) error {
	var rows pgtype.Int4
	if rowsAffected != nil {
		rows = pgtype.Int4{Int32: int32(*rowsAffected), Valid: true}
	}
	return r.q.FinishJobRun(ctx, FinishJobRunParams{
		ID:           id,
		Status:       status,
		RowsAffected: rows,
		Error:        textParam(errText),
		Stats:        stats,
	})
}

func (r *AdminRepo) ListJobRuns(ctx context.Context, limit int, beforeID *int64) ([]admin.JobRun, error) {
	var before pgtype.Int8
	if beforeID != nil {
		before = pgtype.Int8{Int64: *beforeID, Valid: true}
	}
	rows, err := r.q.ListJobRuns(ctx, ListJobRunsParams{Limit: int32(limit), BeforeID: before})
	if err != nil {
		return nil, err
	}
	out := make([]admin.JobRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.JobRun{
			ID:           row.ID,
			Job:          row.Job,
			StartedAt:    row.StartedAt.UTC(),
			FinishedAt:   tsPtr(row.FinishedAt),
			Status:       row.Status,
			RowsAffected: intPtr(row.RowsAffected),
			Error:        textPtr(row.Error),
		})
	}
	return out, nil
}

func (r *AdminRepo) ListJobSchedules(ctx context.Context) ([]admin.JobSchedule, error) {
	rows, err := r.q.ListJobSchedules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.JobSchedule, 0, len(rows))
	for _, row := range rows {
		out = append(out, jobScheduleFromRow(row))
	}
	return out, nil
}

func (r *AdminRepo) GetJobSchedule(ctx context.Context, job string) (admin.JobSchedule, error) {
	row, err := r.q.GetJobSchedule(ctx, job)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.JobSchedule{}, admin.ErrJobScheduleNotFound
	}
	if err != nil {
		return admin.JobSchedule{}, err
	}
	return jobScheduleFromRow(row), nil
}

func (r *AdminRepo) UpdateJobSchedule(ctx context.Context, job string, intervalSeconds int64) (admin.JobSchedule, error) {
	row, err := r.q.UpdateJobSchedule(ctx, UpdateJobScheduleParams{
		Job:             job,
		IntervalSeconds: int32(intervalSeconds),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.JobSchedule{}, admin.ErrJobScheduleNotFound
	}
	if err != nil {
		return admin.JobSchedule{}, err
	}
	return jobScheduleFromRow(row), nil
}

func jobScheduleFromRow(row TWorkerJobSchedule) admin.JobSchedule {
	return admin.JobSchedule{
		Job:             row.Job,
		IntervalSeconds: int64(row.IntervalSeconds),
		Enabled:         row.Enabled,
		OffsetSeconds:   int64(row.OffsetSeconds),
		UpdatedAt:       row.UpdatedAt.UTC(),
	}
}

// ---- dashboard ----

func (r *AdminRepo) Overview(ctx context.Context) (admin.Overview, error) {
	var o admin.Overview
	queries := []struct {
		dst *int64
		fn  func(context.Context) (int64, error)
	}{
		{&o.ActiveSpots, r.q.CountActiveSpots},
		{&o.SpotsWithFreshScore, r.q.CountSpotsWithScore},
		{&o.Devices, r.q.CountDevices},
		{&o.DevicesActive24h, r.q.CountDevicesActive24h},
		{&o.PendingReports, r.q.CountPendingReports},
		{&o.Reports24h, r.q.CountReportsToday},
		{&o.CrowdReports24h, r.q.CountCrowdReportsToday},
		{&o.EnabledAlertRules, r.q.CountEnabledAlertRulesAdmin},
		{&o.AlertDeliveries24h, r.q.CountAlertDeliveriesToday},
		{&o.PlusDevices, r.q.CountPlusDevices},
	}
	for _, item := range queries {
		n, err := item.fn(ctx)
		if err != nil {
			return o, err
		}
		*item.dst = n
	}
	return o, nil
}

func (r *AdminRepo) Freshness(ctx context.Context) (admin.Freshness, error) {
	var f admin.Freshness
	get := func(v any) *time.Time {
		if t, ok := v.(time.Time); ok && !t.IsZero() && t.Year() > 1970 {
			t = t.UTC()
			return &t
		}
		return nil
	}
	var err error
	var w, s, c any
	if w, err = r.q.MaxWeatherFetchedAt(ctx); err != nil {
		return f, err
	}
	if s, err = r.q.MaxScoreComputedAt(ctx); err != nil {
		return f, err
	}
	if c, err = r.q.MaxCrowdUpdatedAt(ctx); err != nil {
		return f, err
	}
	f.WeatherFetchedAt = get(w)
	f.ScoreComputedAt = get(s)
	f.CrowdUpdatedAt = get(c)
	return f, nil
}

func (r *AdminRepo) ListPendingReports(ctx context.Context, limit int, after *admin.ReportCursor) ([]admin.PendingReport, error) {
	var afterTs pgtype.Timestamptz
	var afterID pgtype.UUID
	if after != nil {
		afterTs = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		afterID = pgtype.UUID{Bytes: after.ID, Valid: true}
	}
	rows, err := r.q.ListPendingSpotReports(ctx, ListPendingSpotReportsParams{
		Limit:   int32(limit),
		AfterTs: afterTs,
		AfterID: afterID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.PendingReport, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.PendingReport{
			ID:        row.ID,
			SpotID:    row.SpotID,
			SpotName:  row.Name.String,
			Type:      row.Type,
			IssueCode: textPtr(row.IssueCode),
			CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return out, nil
}

func (r *AdminRepo) UpdateReportStatus(ctx context.Context, id uuid.UUID, status string) (bool, error) {
	n, err := r.q.UpdateSpotReportStatus(ctx, UpdateSpotReportStatusParams{ID: id, Status: status})
	return n > 0, err
}

func (r *AdminRepo) SpotReportTarget(ctx context.Context, id uuid.UUID) (uuid.UUID, string, error) {
	row, err := r.q.GetSpotReportTarget(ctx, id)
	if err != nil {
		return uuid.Nil, "", err
	}
	return row.SpotID, row.Type, nil
}

func (r *AdminRepo) CloseSpotForReport(ctx context.Context, spotID uuid.UUID) error {
	_, err := r.q.CloseSpotForReport(ctx, spotID)
	return err
}

func uuidParamOpt(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
}

func strParamOpt(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func (r *AdminRepo) Metrics(ctx context.Context, f admin.MetricsFilter) (admin.Metrics, error) {
	var m admin.Metrics

	scoreParams := ScoreDistributionParams{
		FromTs: f.From, ToTs: f.To,
		CityPackID: uuidParamOpt(f.CityPackID),
		Kind:       strParamOpt(f.Kind),
	}
	dist, err := r.q.ScoreDistribution(ctx, scoreParams)
	if err != nil {
		return m, err
	}
	m.Score.TotalRows = dist.Total
	m.Score.AvgScore = dist.AvgScore
	m.Score.CappedRows = dist.Capped
	m.Score.Spots = dist.Spots
	hist, err := r.q.ScoreHistogram(ctx, ScoreHistogramParams(scoreParams))
	if err != nil {
		return m, err
	}
	for _, row := range hist {
		if row.Bucket >= 1 && row.Bucket <= 10 {
			m.Score.Histogram[row.Bucket-1] = row.N
		}
	}

	fb, err := r.q.FeedbackVerdictCounts(ctx, FeedbackVerdictCountsParams{
		FromTs: f.From, ToTs: f.To, SpotID: uuidParamOpt(f.SpotID),
	})
	if err != nil {
		return m, err
	}
	for _, row := range fb {
		m.Feedback = append(m.Feedback, admin.VerdictCount{Verdict: row.Verdict, Count: row.N})
	}

	rp, err := r.q.ReportTypeCounts(ctx, ReportTypeCountsParams{
		FromTs: f.From, ToTs: f.To,
		Type: strParamOpt(f.ReportType), Status: strParamOpt(f.ReportStatus),
	})
	if err != nil {
		return m, err
	}
	for _, row := range rp {
		m.Reports = append(m.Reports, admin.ReportCount{Type: row.Type, Status: row.Status, Count: row.N})
	}

	cs, err := r.q.CrowdSourceCounts(ctx)
	if err != nil {
		return m, err
	}
	m.Crowd = admin.CrowdCounts{Model: cs.Model, Community: cs.Community, Provider: cs.Provider}

	ss, err := r.q.SubscriptionStatusCounts(ctx)
	if err != nil {
		return m, err
	}
	for _, row := range ss {
		m.Subscriptions = append(m.Subscriptions, admin.StatusCount{Status: row.Status, Count: row.N})
	}

	dc, err := r.q.AlertDeliveryStatusCounts(ctx, AlertDeliveryStatusCountsParams{
		FromTs: pgtype.Timestamptz{Time: f.From, Valid: true},
		ToTs:   pgtype.Timestamptz{Time: f.To, Valid: true},
	})
	if err != nil {
		return m, err
	}
	for _, row := range dc {
		m.Deliveries = append(m.Deliveries, admin.DeliveryCount{APNsStatus: row.ApnsStatus, Count: row.N})
	}
	return m, nil
}

func (r *AdminRepo) EventSummary(ctx context.Context, f admin.MetricsFilter) (admin.EventSummary, error) {
	var s admin.EventSummary
	s.From, s.To = f.From, f.To
	names, err := r.q.AdminEventNameCounts(ctx, AdminEventNameCountsParams{
		FromTs: f.From, ToTs: f.To, Name: strParamOpt(f.EventName),
	})
	if err != nil {
		return s, err
	}
	for _, row := range names {
		s.ByName = append(s.ByName, admin.NameCount{Name: row.Name, Count: row.N})
	}
	days, err := r.q.AdminEventDayCounts(ctx, AdminEventDayCountsParams{
		FromTs: f.From, ToTs: f.To,
	})
	if err != nil {
		return s, err
	}
	for _, row := range days {
		s.ByDay = append(s.ByDay, admin.DayCount{Day: row.Day.UTC(), Count: row.N})
	}
	return s, nil
}

// ---- scoring config ----

func (r *AdminRepo) ListScoringConfigs(ctx context.Context) ([]admin.ScoringConfigVersion, error) {
	rows, err := r.q.ListScoringConfigs(ctx)
	if err != nil {
		return nil, err
	}
	// The effective config is the one the scorer picks: latest
	// (active_from, version) among rows already active.
	var bestFrom time.Time
	var bestVersion int16 = -1
	now := time.Now()
	for _, row := range rows {
		if row.ActiveFrom.After(now) {
			continue
		}
		if row.ActiveFrom.After(bestFrom) ||
			(row.ActiveFrom.Equal(bestFrom) && row.Version > bestVersion) {
			bestFrom, bestVersion = row.ActiveFrom, row.Version
		}
	}
	out := make([]admin.ScoringConfigVersion, 0, len(rows))
	for _, row := range rows {
		out = append(out, admin.ScoringConfigVersion{
			Version:    row.Version,
			Weights:    row.Weights,
			Thresholds: row.Thresholds,
			ActiveFrom: row.ActiveFrom.UTC(),
			IsActive:   row.Version == bestVersion && row.ActiveFrom.Equal(bestFrom),
		})
	}
	return out, nil
}

func (r *AdminRepo) NextScoringConfigVersion(ctx context.Context) (int16, error) {
	v, err := r.q.NextScoringConfigVersion(ctx)
	return int16(v), err
}

func (r *AdminRepo) InsertScoringConfig(ctx context.Context, version int16, weights, thresholds []byte, activeFrom time.Time) error {
	return r.q.InsertScoringConfig(ctx, InsertScoringConfigParams{
		Version:    version,
		Weights:    weights,
		Thresholds: thresholds,
		ActiveFrom: activeFrom,
	})
}

func (r *AdminRepo) ActivateScoringConfig(ctx context.Context, version int16) (bool, error) {
	n, err := r.q.ActivateScoringConfig(ctx, version)
	return n > 0, err
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

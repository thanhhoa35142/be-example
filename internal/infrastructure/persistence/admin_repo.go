package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
)

type AdminRepo struct {
	q    *Queries
	pool *pgxpool.Pool
}

func NewAdminRepo(q *Queries, pool *pgxpool.Pool) *AdminRepo {
	return &AdminRepo{q: q, pool: pool}
}

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

func (r *AdminRepo) ListJobRuns(ctx context.Context, limit int, beforeID *int64, status string) ([]admin.JobRun, error) {
	var before pgtype.Int8
	if beforeID != nil {
		before = pgtype.Int8{Int64: *beforeID, Valid: true}
	}
	var st pgtype.Text
	if status != "" {
		st = pgtype.Text{String: status, Valid: true}
	}
	rows, err := r.q.ListJobRuns(ctx, ListJobRunsParams{Limit: int32(limit), BeforeID: before, Status: st})
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

func (r *AdminRepo) GetJobRun(ctx context.Context, id int64) (admin.JobRun, error) {
	row, err := r.q.GetJobRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.JobRun{}, admin.ErrJobRunNotFound
	}
	if err != nil {
		return admin.JobRun{}, err
	}
	return jobRunFromModel(row), nil
}

func jobRunFromModel(row TJobRun) admin.JobRun {
	var packID *string
	if row.CityPackID.Valid {
		s := uuid.UUID(row.CityPackID.Bytes).String()
		packID = &s
	}
	var stats json.RawMessage
	if len(row.Stats) > 0 {
		stats = json.RawMessage(row.Stats)
	}
	return admin.JobRun{
		ID:           row.ID,
		Job:          row.Job,
		CityPackID:   packID,
		StartedAt:    row.StartedAt.UTC(),
		FinishedAt:   tsPtr(row.FinishedAt),
		Status:       row.Status,
		RowsAffected: intPtr(row.RowsAffected),
		Error:        textPtr(row.Error),
		Stats:        stats,
	}
}

func (r *AdminRepo) InsertAuditEvent(ctx context.Context, e admin.AuditEvent) error {
	var uid pgtype.UUID
	if e.AdminUserID != nil {
		parsed, err := uuid.Parse(*e.AdminUserID)
		if err != nil {
			return fmt.Errorf("audit admin_user_id: %w", err)
		}
		uid = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	var reqID pgtype.Text
	if e.RequestID != nil {
		reqID = pgtype.Text{String: *e.RequestID, Valid: true}
	}
	return r.q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		AdminUserID: uid,
		AuthMethod:  e.AuthMethod,
		Method:      e.Method,
		Path:        e.Path,
		Status:      int32(e.Status),
		ClientIp:    e.ClientIP,
		RequestID:   reqID,
	})
}

func (r *AdminRepo) ListAuditEvents(ctx context.Context, limit int, beforeID *int64) ([]admin.AuditEvent, error) {
	var before pgtype.Int8
	if beforeID != nil {
		before = pgtype.Int8{Int64: *beforeID, Valid: true}
	}
	rows, err := r.q.ListAdminAuditEvents(ctx, ListAdminAuditEventsParams{Limit: int32(limit), BeforeID: before})
	if err != nil {
		return nil, err
	}
	out := make([]admin.AuditEvent, 0, len(rows))
	for _, row := range rows {
		var uid, email, reqID *string
		if row.AdminUserID.Valid {
			s := uuid.UUID(row.AdminUserID.Bytes).String()
			uid = &s
		}
		if row.Email.Valid {
			s := row.Email.String
			email = &s
		}
		if row.RequestID.Valid {
			s := row.RequestID.String
			reqID = &s
		}
		out = append(out, admin.AuditEvent{
			ID:          row.ID,
			AdminUserID: uid,
			Email:       email,
			AuthMethod:  row.AuthMethod,
			Method:      row.Method,
			Path:        row.Path,
			Status:      int(row.Status),
			ClientIP:    row.ClientIp,
			RequestID:   reqID,
			CreatedAt:   row.CreatedAt.UTC(),
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

func (r *AdminRepo) SetJobScheduleEnabled(ctx context.Context, job string, enabled bool) (admin.JobSchedule, error) {
	row, err := r.q.SetJobScheduleEnabled(ctx, SetJobScheduleEnabledParams{Job: job, Enabled: enabled})
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
		RunRequestedAt:  timePtr(row.RunRequestedAt),
	}
}

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

func (r *AdminRepo) ModerateReport(ctx context.Context, id uuid.UUID, status string) (admin.ModReportOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return admin.ModReportDone, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)

	n, err := q.UpdateSpotReportStatus(ctx, UpdateSpotReportStatusParams{ID: id, Status: status})
	if err != nil {
		return admin.ModReportDone, err
	}
	if n == 0 {
		// Zero rows means the report is missing or already moderated — check
		// which so the caller can answer 404 vs 409.
		if _, err := q.GetSpotReportTarget(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return admin.ModReportNotFound, nil
		} else if err != nil {
			return admin.ModReportDone, err
		}
		return admin.ModReportConflict, nil
	}
	if status == "accepted" {
		target, err := q.GetSpotReportTarget(ctx, id)
		if err != nil {
			return admin.ModReportDone, err
		}
		if target.Type == "closed" {
			if _, err := q.CloseSpotForReport(ctx, target.SpotID); err != nil {
				return admin.ModReportDone, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return admin.ModReportDone, err
	}
	return admin.ModReportDone, nil
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
	// Slices must be non-nil: a nil slice marshals to null and the console's
	// zod schemas expect arrays.
	m := admin.Metrics{
		Feedback:      []admin.VerdictCount{},
		Reports:       []admin.ReportCount{},
		Subscriptions: []admin.StatusCount{},
		Deliveries:    []admin.DeliveryCount{},
	}

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
	s := admin.EventSummary{
		ByName: []admin.NameCount{},
		ByDay:  []admin.DayCount{},
	}
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

func (r *AdminRepo) EventBreakdown(ctx context.Context, in admin.EventBreakdownInput) (admin.EventBreakdown, error) {
	out := admin.EventBreakdown{Name: in.Name, Prop: in.Prop, By: in.By, Since: in.Since, Items: []admin.EventBreakdownItem{}}
	rows, err := r.q.AdminEventPropBreakdown(ctx, AdminEventPropBreakdownParams{
		Prop: in.Prop, By: in.By, Name: in.Name, Since: in.Since,
	})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		it := admin.EventBreakdownItem{Value: row.Value, Count: row.N, Devices: row.Devices}
		if in.By != "" {
			by := row.ByValue
			it.By = &by
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func (r *AdminRepo) ListScoringConfigs(ctx context.Context) ([]admin.ScoringConfigVersion, error) {
	rows, err := r.q.ListScoringConfigs(ctx)
	if err != nil {
		return nil, err
	}

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

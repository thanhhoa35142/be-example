package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/scoring"
)

var weightKeys = map[string]bool{
	"weather": true, "crowd": true, "amenity": true, "shade": true, "fresh": true,
}

var thresholdKeys = map[string]bool{
	"rain_prob_threshold": true, "rain_score_cap": true,
	"heat_cap_c": true, "heat_score_cap": true,
	"cold_cap_c": true, "cold_score_cap": true,
}

type jobScheduleLimit struct {
	min  int64
	max  int64
	desc string
}

var jobScheduleLimits = map[string]jobScheduleLimit{
	// weather.refresh: provider quota (9000 calls/day) over ~1070 cells only
	// affords ~8 runs/day — 3h is the sane floor, 6h the ceiling.
	"weather.refresh":        {min: 15 * 60, max: 6 * 60 * 60, desc: "Fetch weather forecasts for the coming days"},
	"crowd.refresh":          {min: 60 * 60, max: 30 * 24 * 60 * 60, desc: "Refresh provider crowd estimates"},
	"crowd.aggregate":        {min: 60, max: 24 * 60 * 60, desc: "Aggregate community crowd reports"},
	"scoring.recompute":      {min: 5 * 60, max: 6 * 60 * 60, desc: "Recompute spot scores for the forecast window"},
	"alerts.evaluate":        {min: 60, max: 60 * 60, desc: "Evaluate alert rules and queue notifications"},
	"digest.weekly":          {min: 15 * 60, max: 60 * 60, desc: "Send scheduled digest notifications"},
	"holidays.refresh":       {min: 24 * 60 * 60, max: 30 * 24 * 60 * 60, desc: "Refresh the public-holiday calendar"},
	"retention.purge":        {min: 60 * 60, max: 30 * 24 * 60 * 60, desc: "Delete expired job runs, events and reports"},
	"ingest.osm":             {min: 60 * 60, max: 7 * 24 * 60 * 60, desc: "Import OSM city packs"},
	"ingest.extract.refresh": {min: 24 * 60 * 60, max: 30 * 24 * 60 * 60, desc: "Download fresh OSM extracts"},
	"google.sync":            {min: 60 * 60, max: 7 * 24 * 60 * 60, desc: "Sync Search Console + GA4 traffic data"},
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) RecordJobStart(ctx context.Context, job string) (int64, error) {
	return s.repo.StartJobRun(ctx, job)
}

func (s *Service) AbandonJobRuns(ctx context.Context, job string) error {
	return s.repo.AbandonJobRuns(ctx, job)
}

func (s *Service) RecordJobFinish(ctx context.Context, id int64, status string, rowsAffected *int, jobErr error, stats []byte) {
	var errText *string
	if jobErr != nil {
		msg := jobErr.Error()
		errText = &msg
	}

	_ = s.repo.FinishJobRun(context.Background(), id, status, rowsAffected, errText, stats)
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	return s.repo.Overview(ctx)
}

func (s *Service) Freshness(ctx context.Context) (Freshness, error) {
	return s.repo.Freshness(ctx)
}

func normalizeFilter(f MetricsFilter, defaultDays int) (MetricsFilter, error) {
	if f.To.IsZero() {
		f.To = time.Now().UTC()
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-time.Duration(defaultDays) * 24 * time.Hour)
	}
	f.From, f.To = f.From.UTC(), f.To.UTC()
	if !f.From.Before(f.To) {
		return f, ErrInvalidFilter
	}
	if f.To.Sub(f.From) > 366*24*time.Hour {
		return f, ErrInvalidFilter
	}
	return f, nil
}

func (s *Service) Metrics(ctx context.Context, f MetricsFilter) (Metrics, error) {
	f, err := normalizeFilter(f, 7)
	if err != nil {
		return Metrics{}, err
	}
	return s.repo.Metrics(ctx, f)
}

func (s *Service) EventSummary(ctx context.Context, f MetricsFilter) (EventSummary, error) {
	f, err := normalizeFilter(f, 30)
	if err != nil {
		return EventSummary{}, err
	}
	return s.repo.EventSummary(ctx, f)
}

var (
	eventNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	propKeyRe   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)
)

func (s *Service) EventBreakdown(ctx context.Context, in EventBreakdownInput, now time.Time) (EventBreakdown, error) {
	if !eventNameRe.MatchString(in.Name) {
		return EventBreakdown{}, fmt.Errorf("%w: name must match [a-z][a-z0-9_]{0,63}", ErrInvalidFilter)
	}
	if !propKeyRe.MatchString(in.Prop) {
		return EventBreakdown{}, fmt.Errorf("%w: prop must match [a-zA-Z][a-zA-Z0-9_]{0,31}", ErrInvalidFilter)
	}
	if in.By != "" && !propKeyRe.MatchString(in.By) {
		return EventBreakdown{}, fmt.Errorf("%w: by must match [a-zA-Z][a-zA-Z0-9_]{0,31}", ErrInvalidFilter)
	}
	if in.Since.IsZero() {
		in.Since = now.Add(-30 * 24 * time.Hour)
	}
	in.Since = in.Since.UTC()
	return s.repo.EventBreakdown(ctx, in)
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}

func (s *Service) Jobs(ctx context.Context, limit int, beforeID *int64, status string) (Page[JobRun], error) {
	limit = clampLimit(limit)
	rows, err := s.repo.ListJobRuns(ctx, limit+1, beforeID, status)
	if err != nil {
		return Page[JobRun]{}, err
	}
	if len(rows) > limit {
		cur := strconv.FormatInt(rows[limit-1].ID, 10)
		return Page[JobRun]{Items: rows[:limit], NextCursor: &cur}, nil
	}
	return Page[JobRun]{Items: rows}, nil
}

func (s *Service) JobRun(ctx context.Context, id int64) (JobRun, error) {
	return s.repo.GetJobRun(ctx, id)
}

func (s *Service) RecordAudit(ctx context.Context, e AuditEvent) error {
	return s.repo.InsertAuditEvent(ctx, e)
}

func (s *Service) AuditEvents(ctx context.Context, limit int, beforeID *int64) (Page[AuditEvent], error) {
	limit = clampLimit(limit)
	rows, err := s.repo.ListAuditEvents(ctx, limit+1, beforeID)
	if err != nil {
		return Page[AuditEvent]{}, err
	}
	if len(rows) > limit {
		cur := strconv.FormatInt(rows[limit-1].ID, 10)
		return Page[AuditEvent]{Items: rows[:limit], NextCursor: &cur}, nil
	}
	return Page[AuditEvent]{Items: rows}, nil
}

func (s *Service) JobSchedules(ctx context.Context) ([]JobSchedule, error) {
	schedules, err := s.repo.ListJobSchedules(ctx)
	if err != nil {
		return nil, err
	}
	if len(schedules) != len(jobScheduleLimits) {
		return nil, fmt.Errorf("%w: expected %d registered job schedules, found %d",
			ErrInvalidJobSchedule, len(jobScheduleLimits), len(schedules))
	}
	for i := range schedules {
		limits, ok := jobScheduleLimits[schedules[i].Job]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrJobScheduleNotFound, schedules[i].Job)
		}
		if err := validateJobInterval(schedules[i].IntervalSeconds, limits); err != nil {
			return nil, fmt.Errorf("%s: %w", schedules[i].Job, err)
		}
		schedules[i].MinIntervalSeconds = limits.min
		schedules[i].MaxIntervalSeconds = limits.max
		schedules[i].Description = limits.desc
	}
	return schedules, nil
}

func (s *Service) JobSchedule(ctx context.Context, job string) (JobSchedule, error) {
	limits, ok := jobScheduleLimits[job]
	if !ok {
		return JobSchedule{}, ErrJobScheduleNotFound
	}
	schedule, err := s.repo.GetJobSchedule(ctx, job)
	if err != nil {
		return JobSchedule{}, err
	}
	if err := validateJobInterval(schedule.IntervalSeconds, limits); err != nil {
		return JobSchedule{}, fmt.Errorf("%s: %w", job, err)
	}
	schedule.MinIntervalSeconds = limits.min
	schedule.MaxIntervalSeconds = limits.max
	schedule.Description = limits.desc
	return schedule, nil
}

func (s *Service) UpdateJobSchedule(ctx context.Context, job string, intervalSeconds int64) (JobSchedule, error) {
	limits, ok := jobScheduleLimits[job]
	if !ok {
		return JobSchedule{}, ErrJobScheduleNotFound
	}
	if err := validateJobInterval(intervalSeconds, limits); err != nil {
		return JobSchedule{}, err
	}
	schedule, err := s.repo.UpdateJobSchedule(ctx, job, intervalSeconds)
	if err != nil {
		return JobSchedule{}, err
	}
	schedule.MinIntervalSeconds = limits.min
	schedule.MaxIntervalSeconds = limits.max
	schedule.Description = limits.desc
	return schedule, nil
}

func validateJobInterval(seconds int64, limits jobScheduleLimit) error {
	if seconds < limits.min || seconds > limits.max {
		return fmt.Errorf("%w: interval must be between %d and %d seconds",
			ErrInvalidJobSchedule, limits.min, limits.max)
	}
	return nil
}

func (s *Service) PendingReports(ctx context.Context, limit int, after *ReportCursor) (Page[PendingReport], error) {
	limit = clampLimit(limit)
	rows, err := s.repo.ListPendingReports(ctx, limit+1, after)
	if err != nil {
		return Page[PendingReport]{}, err
	}
	if len(rows) > limit {
		cur := ReportCursor{CreatedAt: rows[limit-1].CreatedAt, ID: rows[limit-1].ID}.Encode()
		return Page[PendingReport]{Items: rows[:limit], NextCursor: &cur}, nil
	}
	return Page[PendingReport]{Items: rows}, nil
}

func (s *Service) ModerateReport(ctx context.Context, id uuid.UUID, status string) error {
	if status != "accepted" && status != "rejected" {
		return ErrInvalidStatus
	}
	outcome, err := s.repo.ModerateReport(ctx, id, status)
	if err != nil {
		return err
	}
	switch outcome {
	case ModReportNotFound:
		return ErrReportNotFound
	case ModReportConflict:
		return ErrReportConflict
	}
	return nil
}

func (s *Service) ListScoringConfigs(ctx context.Context) ([]ScoringConfigVersion, error) {
	return s.repo.ListScoringConfigs(ctx)
}

func (s *Service) CreateScoringConfig(ctx context.Context, in ScoringConfigInput) (*ScoringConfigVersion, error) {
	if err := validateConfigJSON(in.Weights, weightKeys, "weights"); err != nil {
		return nil, err
	}
	// A non-positive weight sum would make Score() divide by ~zero and the
	// batch upsert violate the 0-100 CHECK — reject at write time.
	var w scoring.Weights
	if err := json.Unmarshal(in.Weights, &w); err != nil {
		return nil, fmt.Errorf("%w: weights must be numeric", ErrInvalidConfig)
	}
	wsum := w.Weather + w.Crowd + w.Amenity + w.Shade + w.Fresh
	if wsum <= 0 {
		return nil, fmt.Errorf("%w: weights must sum to a positive value", ErrInvalidConfig)
	}
	if err := validateConfigJSON(in.Thresholds, thresholdKeys, "thresholds"); err != nil {
		return nil, err
	}
	version, err := s.repo.NextScoringConfigVersion(ctx)
	if err != nil {
		return nil, err
	}
	activeFrom := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.Activate {
		activeFrom = time.Now().UTC()
	}
	if err := s.repo.InsertScoringConfig(ctx, version, in.Weights, in.Thresholds, activeFrom); err != nil {
		return nil, err
	}
	cfgs, err := s.repo.ListScoringConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cfgs {
		if c.Version == version {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("config version %d not found after insert", version)
}

func (s *Service) ActivateScoringConfig(ctx context.Context, version int16) error {
	ok, err := s.repo.ActivateScoringConfig(ctx, version)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConfigNotFound
	}
	return nil
}

func validateConfigJSON(raw json.RawMessage, required map[string]bool, name string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return fmt.Errorf("%w: %s must be a JSON object", ErrInvalidConfig, name)
	}
	for k := range required {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("%w: %s missing key %q", ErrInvalidConfig, name, k)
		}
	}
	return nil
}

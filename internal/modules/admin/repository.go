package admin

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence port for the admin module.
type Repository interface {
	// Job instrumentation
	StartJobRun(ctx context.Context, job string) (int64, error)
	FinishJobRun(ctx context.Context, id int64, status string, rowsAffected *int, errText *string) error
	// AbandonJobRuns marks orphaned 'running' rows of a job as 'abandoned'.
	// Called after the job wins its advisory lock.
	AbandonJobRuns(ctx context.Context, job string) error
	ListJobRuns(ctx context.Context, limit int, beforeID *int64) ([]JobRun, error)
	ListJobSchedules(ctx context.Context) ([]JobSchedule, error)
	GetJobSchedule(ctx context.Context, job string) (JobSchedule, error)
	UpdateJobSchedule(ctx context.Context, job string, intervalSeconds int64) (JobSchedule, error)

	// Dashboard reads
	Overview(ctx context.Context) (Overview, error)
	Freshness(ctx context.Context) (Freshness, error)
	ListPendingReports(ctx context.Context, limit int, after *ReportCursor) ([]PendingReport, error)
	UpdateReportStatus(ctx context.Context, id uuid.UUID, status string) (bool, error)
	// SpotReportTarget returns the spot_id and type of a spot_report.
	SpotReportTarget(ctx context.Context, id uuid.UUID) (spotID uuid.UUID, typ string, err error)
	// CloseSpotForReport marks a spot closed with reason 'closed_by_report'
	// (no-op unless the spot is active/pending).
	CloseSpotForReport(ctx context.Context, spotID uuid.UUID) error

	// Analytics
	Metrics(ctx context.Context, f MetricsFilter) (Metrics, error)
	EventSummary(ctx context.Context, f MetricsFilter) (EventSummary, error)

	// Scoring config management
	ListScoringConfigs(ctx context.Context) ([]ScoringConfigVersion, error)
	NextScoringConfigVersion(ctx context.Context) (int16, error)
	InsertScoringConfig(ctx context.Context, version int16, weights, thresholds []byte, activeFrom time.Time) error
	ActivateScoringConfig(ctx context.Context, version int16) (bool, error)
}

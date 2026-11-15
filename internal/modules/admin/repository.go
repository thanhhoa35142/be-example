package admin

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	StartJobRun(ctx context.Context, job string) (int64, error)
	FinishJobRun(ctx context.Context, id int64, status string, rowsAffected *int, errText *string, stats []byte) error

	AbandonJobRuns(ctx context.Context, job string) error
	ListJobRuns(ctx context.Context, limit int, beforeID *int64, status string) ([]JobRun, error)
	GetJobRun(ctx context.Context, id int64) (JobRun, error)
	InsertAuditEvent(ctx context.Context, e AuditEvent) error
	ListAuditEvents(ctx context.Context, limit int, beforeID *int64) ([]AuditEvent, error)
	ListJobSchedules(ctx context.Context) ([]JobSchedule, error)
	GetJobSchedule(ctx context.Context, job string) (JobSchedule, error)
	UpdateJobSchedule(ctx context.Context, job string, intervalSeconds int64) (JobSchedule, error)

	Overview(ctx context.Context) (Overview, error)
	Freshness(ctx context.Context) (Freshness, error)
	ListPendingReports(ctx context.Context, limit int, after *ReportCursor) ([]PendingReport, error)
	UpdateReportStatus(ctx context.Context, id uuid.UUID, status string) (bool, error)

	SpotReportTarget(ctx context.Context, id uuid.UUID) (spotID uuid.UUID, typ string, err error)

	CloseSpotForReport(ctx context.Context, spotID uuid.UUID) error

	Metrics(ctx context.Context, f MetricsFilter) (Metrics, error)
	EventSummary(ctx context.Context, f MetricsFilter) (EventSummary, error)
	EventBreakdown(ctx context.Context, in EventBreakdownInput) (EventBreakdown, error)

	ListScoringConfigs(ctx context.Context) ([]ScoringConfigVersion, error)
	NextScoringConfigVersion(ctx context.Context) (int16, error)
	InsertScoringConfig(ctx context.Context, version int16, weights, thresholds []byte, activeFrom time.Time) error
	ActivateScoringConfig(ctx context.Context, version int16) (bool, error)

	RequestJobRun(ctx context.Context, job string) (JobSchedule, error)
	ClaimJobRunRequest(ctx context.Context, job string) (bool, error)
	ListPlanLimits(ctx context.Context) ([]PlanLimit, error)
	UpdatePlanLimit(ctx context.Context, plan, feature string, limit int) (bool, error)
	ListCityPacks(ctx context.Context) ([]CityPack, error)
	CityPackDetail(ctx context.Context, id uuid.UUID) (CityPackDetail, error)

	CityPackStatus(ctx context.Context, id uuid.UUID) (status string, imported bool, err error)
	UpdateCityPackStatus(ctx context.Context, id uuid.UUID, status string) error
	SearchSpots(ctx context.Context, f SpotFilter) ([]AdminSpot, error)
	SpotStatus(ctx context.Context, id uuid.UUID) (string, error)

	SetSpotStatus(ctx context.Context, id uuid.UUID, from []string, status string, reason *string) (bool, error)
}

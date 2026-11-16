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
	SetJobScheduleEnabled(ctx context.Context, job string, enabled bool) (JobSchedule, error)

	Overview(ctx context.Context) (Overview, error)
	Freshness(ctx context.Context) (Freshness, error)
	ListPendingReports(ctx context.Context, limit int, after *ReportCursor) ([]PendingReport, error)
	// ModerateReport transitions a pending report and, for accepted "closed"
	// reports, closes the spot — atomically. Returns the outcome so the
	// service can map missing vs already-moderated reports to 404/409.
	ModerateReport(ctx context.Context, id uuid.UUID, status string) (ModReportOutcome, error)

	Metrics(ctx context.Context, f MetricsFilter) (Metrics, error)
	EventSummary(ctx context.Context, f MetricsFilter) (EventSummary, error)
	EventBreakdown(ctx context.Context, in EventBreakdownInput) (EventBreakdown, error)

	ListScoringConfigs(ctx context.Context) ([]ScoringConfigVersion, error)
	NextScoringConfigVersion(ctx context.Context) (int16, error)
	InsertScoringConfig(ctx context.Context, version int16, weights, thresholds []byte, activeFrom time.Time) error
	ActivateScoringConfig(ctx context.Context, version int16) (bool, error)

	RequestJobRun(ctx context.Context, job string) (JobSchedule, error)
	RequestJobRunForPack(ctx context.Context, job, packSlug string) (JobSchedule, error)
	// ClaimJobRunRequest atomically consumes a pending run request. It returns
	// the requested pack slug ("" for a generic run) and whether a request was
	// claimed.
	ClaimJobRunRequest(ctx context.Context, job string) (packSlug string, claimed bool, err error)
	ListPlanLimits(ctx context.Context) ([]PlanLimit, error)
	UpdatePlanLimit(ctx context.Context, plan, feature string, limit int) (bool, error)
	ListCityPacks(ctx context.Context) ([]CityPack, error)
	CityPackDetail(ctx context.Context, id uuid.UUID) (CityPackDetail, error)

	CityPackStatus(ctx context.Context, id uuid.UUID) (status string, imported bool, err error)
	// UpdateCityPackStatus reports whether a row actually changed.
	UpdateCityPackStatus(ctx context.Context, id uuid.UUID, status string) (bool, error)
	SearchSpots(ctx context.Context, f SpotFilter) ([]AdminSpot, error)
	SpotDetail(ctx context.Context, id uuid.UUID) (*SpotDetail, error)
	SpotStatus(ctx context.Context, id uuid.UUID) (string, error)

	SetSpotStatus(ctx context.Context, id uuid.UUID, from []string, status string, reason *string) (bool, error)

	// Ops lists (moderation history, devices, deliveries, notifications,
	// provider quotas, ingest features).
	ListSpotReports(ctx context.Context, f SpotReportFilter, limit int, before *ReportCursor) ([]SpotReport, error)
	GetSpotReport(ctx context.Context, id uuid.UUID) (*SpotReport, error)
	ListCrowdReports(ctx context.Context, f CrowdReportFilter, limit int, before *ReportCursor) ([]CrowdReport, error)
	ListScoreFeedbacks(ctx context.Context, f ScoreFeedbackFilter, limit int, before *ReportCursor) ([]ScoreFeedback, error)
	ListDevices(ctx context.Context, f AdminDeviceFilter, limit int, before *ReportCursor) ([]AdminDevice, error)
	GetDevice(ctx context.Context, id uuid.UUID) (*AdminDeviceDetail, error)
	ListDeviceSubscriptions(ctx context.Context, deviceID uuid.UUID) ([]DeviceSubscription, error)
	ListAlertDeliveries(ctx context.Context, f DeliveryFilter, limit int, before *ReportCursor) ([]AlertDelivery, error)
	ListAppleNotifications(ctx context.Context, f AppleNotificationFilter, limit int, before *ReportCursor) ([]AppleNotification, error)
	ListProviderQuotas(ctx context.Context, since time.Time) ([]ProviderQuota, error)
	ListJobRunsForJob(ctx context.Context, job string, limit int) ([]JobRun, error)
	ListIngestFeatures(ctx context.Context, f IngestFeatureFilter, limit int, before *IngestFeatureCursor) ([]IngestFeature, error)
	CityPackBySlug(ctx context.Context, slug string) (uuid.UUID, error)

	// Spot lifecycle mutations; each reports whether a row actually changed.
	SetSpotCurated(ctx context.Context, id uuid.UUID, curated bool) (bool, error)
	UpdateSpotName(ctx context.Context, id uuid.UUID, name *string) (bool, error)
	// SpotPackAndStatus returns the spot's pack, status and merge target;
	// ErrSpotNotFound when missing.
	SpotPackAndStatus(ctx context.Context, id uuid.UUID) (packID uuid.UUID, status string, mergedInto *uuid.UUID, err error)
	MergeSpot(ctx context.Context, id, target uuid.UUID) (bool, error)
	UnmergeSpot(ctx context.Context, id uuid.UUID) (bool, error)
}

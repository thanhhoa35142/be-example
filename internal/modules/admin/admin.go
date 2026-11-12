// Package admin provides internal operations: job_run instrumentation,
// overview metrics, spot-report moderation, and scoring_config management.
// Exposed only on the staff surface (ADMIN_TOKEN), never publicly.
package admin

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrReportNotFound      = errors.New("report not found")
	ErrInvalidStatus       = errors.New("invalid report status")
	ErrInvalidConfig       = errors.New("invalid scoring config")
	ErrConfigExists        = errors.New("scoring config version exists")
	ErrConfigNotFound      = errors.New("scoring config version not found")
	ErrInvalidFilter       = errors.New("invalid filter")
	ErrInvalidJobSchedule  = errors.New("invalid job schedule")
	ErrJobScheduleNotFound = errors.New("job schedule not found")
)

// JobRun mirrors job_run rows.
type JobRun struct {
	ID           int64      `json:"id"`
	Job          string     `json:"job"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
	Status       string     `json:"status"`
	RowsAffected *int       `json:"rowsAffected"`
	Error        *string    `json:"error"`
}

// JobSchedule controls the execution interval for a registered worker job.
type JobSchedule struct {
	Job                string    `json:"job"`
	IntervalSeconds    int64     `json:"intervalSeconds"`
	MinIntervalSeconds int64     `json:"minIntervalSeconds"`
	MaxIntervalSeconds int64     `json:"maxIntervalSeconds"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Overview aggregates headline counts for the dashboard.
type Overview struct {
	ActiveSpots         int64 `json:"activeSpots"`
	SpotsWithFreshScore int64 `json:"spotsWithFreshScore"`
	Devices             int64 `json:"devices"`
	DevicesActive24h    int64 `json:"devicesActive24h"`
	PendingReports      int64 `json:"pendingReports"`
	Reports24h          int64 `json:"reports24h"`
	CrowdReports24h     int64 `json:"crowdReports24h"`
	EnabledAlertRules   int64 `json:"enabledAlertRules"`
	AlertDeliveries24h  int64 `json:"alertDeliveries24h"`
	PlusDevices         int64 `json:"plusDevices"`
}

// Freshness reports the newest data timestamp per pipeline stage.
type Freshness struct {
	WeatherFetchedAt *time.Time `json:"weatherFetchedAt"`
	ScoreComputedAt  *time.Time `json:"scoreComputedAt"`
	CrowdUpdatedAt   *time.Time `json:"crowdUpdatedAt"`
}

// PendingReport is a spot_report awaiting moderation.
type PendingReport struct {
	ID        uuid.UUID `json:"id"`
	SpotID    uuid.UUID `json:"spotId"`
	SpotName  string    `json:"spotName"`
	Type      string    `json:"type"`
	IssueCode *string   `json:"issueCode"`
	CreatedAt time.Time `json:"createdAt"`
}

// Metrics aggregates analytical counters for the dashboard.
type Metrics struct {
	Score         ScoreMetrics    `json:"score"`
	Feedback      []VerdictCount  `json:"feedback"`
	Reports       []ReportCount   `json:"reports"`
	Crowd         CrowdCounts     `json:"crowd"`
	Subscriptions []StatusCount   `json:"subscriptions"`
	Deliveries    []DeliveryCount `json:"deliveries"`
}

// ScoreMetrics summarizes spot_score over the last 7 days.
type ScoreMetrics struct {
	TotalRows  int64   `json:"totalRows"`
	AvgScore   float64 `json:"avgScore"`
	CappedRows int64   `json:"cappedRows"`
	Spots      int64   `json:"spots"`
	// Histogram buckets: index 0 = scores 0-9, ..., 9 = 90-100.
	Histogram [10]int64 `json:"histogram"`
}

type VerdictCount struct {
	Verdict int16 `json:"verdict"`
	Count   int64 `json:"count"`
}

type ReportCount struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type CrowdCounts struct {
	Model     int64 `json:"model"`
	Community int64 `json:"community"`
	Provider  int64 `json:"provider"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// DeliveryCount groups alert_delivery by apns_status (-1 = pending/failed send).
type DeliveryCount struct {
	APNsStatus int32 `json:"apnsStatus"`
	Count      int64 `json:"count"`
}

// MetricsFilter scopes analytical queries. All fields optional except the
// time range; nil means "no filter".
type MetricsFilter struct {
	From         time.Time
	To           time.Time
	CityPackID   *uuid.UUID // score metrics only
	Kind         *string    // spot kind: park|beach|site|table
	SpotID       *uuid.UUID // feedback metrics only
	ReportType   *string    // reports only
	ReportStatus *string    // reports only
	EventName    *string    // event summary only
}

// EventSummary aggregates product events over a window.
type EventSummary struct {
	From   time.Time   `json:"from"`
	To     time.Time   `json:"to"`
	ByName []NameCount `json:"byName"`
	ByDay  []DayCount  `json:"byDay"`
}

// NameCount counts events per name.
type NameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// DayCount counts events per UTC day.
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

// ScoringConfigVersion is one scoring_config row.
type ScoringConfigVersion struct {
	Version    int16           `json:"version"`
	Weights    json.RawMessage `json:"weights"`
	Thresholds json.RawMessage `json:"thresholds"`
	ActiveFrom time.Time       `json:"activeFrom"`
	IsActive   bool            `json:"isActive"`
}

// ScoringConfigInput creates a new config version.
type ScoringConfigInput struct {
	Weights    json.RawMessage `json:"weights"`
	Thresholds json.RawMessage `json:"thresholds"`
	Activate   bool            // if true, active_from = now(); else far-future draft
}

package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrReportNotFound      = errors.New("report not found")
	ErrReportConflict      = errors.New("report already moderated")
	ErrInvalidStatus       = errors.New("invalid report status")
	ErrInvalidConfig       = errors.New("invalid scoring config")
	ErrConfigExists        = errors.New("scoring config version exists")
	ErrConfigNotFound      = errors.New("scoring config version not found")
	ErrInvalidFilter       = errors.New("invalid filter")
	ErrInvalidJobSchedule  = errors.New("invalid job schedule")
	ErrJobScheduleNotFound = errors.New("job schedule not found")
	ErrJobRunNotFound      = errors.New("job run not found")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

// ModReportOutcome distinguishes why a moderation attempt did or didn't
// change a report — the handler maps these to 404 vs 409.
type ModReportOutcome int

const (
	ModReportDone ModReportOutcome = iota
	ModReportNotFound
	ModReportConflict
)

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

type ReportCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func (c ReportCursor) Encode() string {
	return fmt.Sprintf("%d|%s", c.CreatedAt.UnixNano(), c.ID.String())
}

func ParseReportCursor(s string) (ReportCursor, error) {
	nanos, idStr, ok := strings.Cut(s, "|")
	if !ok {
		return ReportCursor{}, ErrInvalidCursor
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return ReportCursor{}, ErrInvalidCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return ReportCursor{}, ErrInvalidCursor
	}
	return ReportCursor{CreatedAt: time.Unix(0, n).UTC(), ID: id}, nil
}

type JobRun struct {
	ID           int64           `json:"id"`
	Job          string          `json:"job"`
	CityPackID   *string         `json:"cityPackId"`
	StartedAt    time.Time       `json:"startedAt"`
	FinishedAt   *time.Time      `json:"finishedAt"`
	Status       string          `json:"status"`
	RowsAffected *int            `json:"rowsAffected"`
	Error        *string         `json:"error"`
	Stats        json.RawMessage `json:"stats,omitempty"`
}

// AuditEvent is one recorded admin mutation: method + path + outcome.
// AdminUserID/Email are nil for bearer-token (scripts/CI) calls.
type AuditEvent struct {
	ID          int64     `json:"id"`
	AdminUserID *string   `json:"adminUserId"`
	Email       *string   `json:"email"`
	AuthMethod  string    `json:"authMethod"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Status      int       `json:"status"`
	ClientIP    string    `json:"clientIp"`
	RequestID   *string   `json:"requestId"`
	CreatedAt   time.Time `json:"createdAt"`
}

type JobSchedule struct {
	Job                string    `json:"job"`
	Description        string    `json:"description"`
	IntervalSeconds    int64     `json:"intervalSeconds"`
	Enabled            bool      `json:"enabled"`
	OffsetSeconds      int64     `json:"offsetSeconds"`
	MinIntervalSeconds int64     `json:"minIntervalSeconds"`
	MaxIntervalSeconds int64     `json:"maxIntervalSeconds"`
	UpdatedAt          time.Time `json:"updatedAt"`

	RunRequestedAt *time.Time `json:"runRequestedAt"`
}

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

type Freshness struct {
	WeatherFetchedAt *time.Time `json:"weatherFetchedAt"`
	ScoreComputedAt  *time.Time `json:"scoreComputedAt"`
	CrowdUpdatedAt   *time.Time `json:"crowdUpdatedAt"`
}

type PendingReport struct {
	ID        uuid.UUID `json:"id"`
	SpotID    uuid.UUID `json:"spotId"`
	SpotName  string    `json:"spotName"`
	Type      string    `json:"type"`
	IssueCode *string   `json:"issueCode"`
	CreatedAt time.Time `json:"createdAt"`
}

type Metrics struct {
	Score         ScoreMetrics    `json:"score"`
	Feedback      []VerdictCount  `json:"feedback"`
	Reports       []ReportCount   `json:"reports"`
	Crowd         CrowdCounts     `json:"crowd"`
	Subscriptions []StatusCount   `json:"subscriptions"`
	Deliveries    []DeliveryCount `json:"deliveries"`
}

type ScoreMetrics struct {
	TotalRows  int64   `json:"totalRows"`
	AvgScore   float64 `json:"avgScore"`
	CappedRows int64   `json:"cappedRows"`
	Spots      int64   `json:"spots"`

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

type DeliveryCount struct {
	APNsStatus int32 `json:"apnsStatus"`
	Count      int64 `json:"count"`
}

type MetricsFilter struct {
	From         time.Time
	To           time.Time
	CityPackID   *uuid.UUID
	Kind         *string
	SpotID       *uuid.UUID
	ReportType   *string
	ReportStatus *string
	EventName    *string
}

type EventSummary struct {
	From   time.Time   `json:"from"`
	To     time.Time   `json:"to"`
	ByName []NameCount `json:"byName"`
	ByDay  []DayCount  `json:"byDay"`
}

type EventBreakdownInput struct {
	Name  string
	Prop  string
	By    string
	Since time.Time
}

type EventBreakdown struct {
	Name  string               `json:"name"`
	Prop  string               `json:"prop"`
	By    string               `json:"by,omitempty"`
	Since time.Time            `json:"since"`
	Items []EventBreakdownItem `json:"items"`
}

type EventBreakdownItem struct {
	Value   string  `json:"value"`
	By      *string `json:"by,omitempty"`
	Count   int64   `json:"count"`
	Devices int64   `json:"devices"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

type ScoringConfigVersion struct {
	Version    int16           `json:"version"`
	Weights    json.RawMessage `json:"weights"`
	Thresholds json.RawMessage `json:"thresholds"`
	ActiveFrom time.Time       `json:"activeFrom"`
	IsActive   bool            `json:"isActive"`
}

type ScoringConfigInput struct {
	Weights    json.RawMessage `json:"weights"`
	Thresholds json.RawMessage `json:"thresholds"`
	Activate   bool
}

package reports

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrDeviceNotFound  = errors.New("device not found")
	ErrSpotNotFound    = errors.New("spot not found")
	ErrDuplicateReport = errors.New("duplicate report")
	ErrRateLimited     = errors.New("rate limited")
	ErrInvalidReport   = errors.New("invalid report")
)

const (
	ReportConfirm = "confirm"
	ReportMissing = "missing"
	ReportClosed  = "closed"
	ReportIssue   = "issue"
)

const (
	CrowdQuiet    = 1
	CrowdModerate = 2
	CrowdBusy     = 3
)

const (
	CrowdRateLimit = 30

	SpotReportRateLimit = 30

	FeedbackRateLimit = 100
)

const (
	FeedbackMaxAge   = 72 * time.Hour
	FeedbackMaxAhead = 7*24*time.Hour + time.Hour
)

const MaxIssueCodeLen = 64

type SpotReportInput struct {
	SpotID    uuid.UUID
	DeviceID  uuid.UUID
	Type      string
	IssueCode *string
}

type CrowdReportInput struct {
	SpotID   uuid.UUID
	DeviceID uuid.UUID
	Level    int
}

type ScoreFeedbackInput struct {
	SpotID   uuid.UUID
	DeviceID uuid.UUID
	TSHour   time.Time
	Verdict  int
}

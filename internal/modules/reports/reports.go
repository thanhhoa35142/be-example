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

// Spot report types per spec: confirm / missing / closed / issue.
const (
	ReportConfirm = "confirm"
	ReportMissing = "missing"
	ReportClosed  = "closed"
	ReportIssue   = "issue"
)

// Crowd levels: 1 = quiet, 2 = moderate, 3 = busy.
const (
	CrowdQuiet    = 1
	CrowdModerate = 2
	CrowdBusy     = 3
)

// CrowdRateLimit is the max crowd reports per device per rolling 24h.
const CrowdRateLimit = 30

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
	Verdict  int // e.g. -1/0/+1 or 1-5; validated by handler range
}

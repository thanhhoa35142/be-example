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

// Per-device limits over a rolling 24h window.
const (
	// CrowdRateLimit is the max crowd reports per device.
	CrowdRateLimit = 30
	// SpotReportRateLimit is the max spot reports (any type) per device.
	SpotReportRateLimit = 30
	// FeedbackRateLimit is the max score-feedback submissions per device.
	FeedbackRateLimit = 100
)

// Score feedback may reference an hour this far back (the user is looking at
// a recent hour) or ahead (the 7-day forecast horizon, plus the current hour).
const (
	FeedbackMaxAge   = 72 * time.Hour
	FeedbackMaxAhead = 7*24*time.Hour + time.Hour
)

// MaxIssueCodeLen bounds the issueCode of an "issue" report.
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
	Verdict  int // e.g. -1/0/+1 or 1-5; validated by handler range
}

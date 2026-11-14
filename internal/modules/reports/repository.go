package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the persistence port for the reports module.
type Repository interface {
	// DeviceActive reports whether the device exists and is not deleted.
	DeviceActive(ctx context.Context, deviceID uuid.UUID) (bool, error)
	// DeviceTZ returns the device's IANA timezone for local slot derivation.
	DeviceTZ(ctx context.Context, deviceID uuid.UUID) (string, error)
	// SpotActive reports whether the spot exists and is active.
	SpotActive(ctx context.Context, spotID uuid.UUID) (bool, error)
	// InsertSpotReport inserts a spot report; returns ErrDuplicateReport
	// when the (device, spot, type, day) unique constraint hits.
	InsertSpotReport(ctx context.Context, in SpotReportInput) error
	// SpotReportsSince counts a device's spot reports after `since`.
	SpotReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)
	// ScoreFeedbackSince counts a device's feedback rows created after `since`.
	ScoreFeedbackSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)
	// CrowdReportsSince counts a device's crowd reports after `since`.
	CrowdReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)
	// InsertCrowdReport stores a community crowd report.
	InsertCrowdReport(ctx context.Context, spotID, deviceID uuid.UUID, level, localDow, localHour int) error
	// InsertScoreFeedback stores a "is this score right?" verdict.
	InsertScoreFeedback(ctx context.Context, in ScoreFeedbackInput) error
}

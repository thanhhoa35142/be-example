package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	DeviceActive(ctx context.Context, deviceID uuid.UUID) (bool, error)

	DeviceTZ(ctx context.Context, deviceID uuid.UUID) (string, error)

	SpotActive(ctx context.Context, spotID uuid.UUID) (bool, error)

	InsertSpotReport(ctx context.Context, in SpotReportInput) error

	SpotReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)

	ScoreFeedbackSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)

	CrowdReportsSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int, error)

	InsertCrowdReport(ctx context.Context, spotID, deviceID uuid.UUID, level, localDow, localHour int) error

	InsertScoreFeedback(ctx context.Context, in ScoreFeedbackInput) error
}

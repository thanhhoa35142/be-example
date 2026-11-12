package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// TrackBatch validates and stores a batch of events for a device.
func (s *Service) TrackBatch(ctx context.Context, deviceID uuid.UUID, batch []Input) error {
	if len(batch) == 0 || len(batch) > MaxBatchSize {
		return fmt.Errorf("%w: batch must contain 1..%d events", ErrInvalidEvent, MaxBatchSize)
	}
	for _, in := range batch {
		if err := validate(in); err != nil {
			return err
		}
	}
	exists, err := s.repo.DeviceExists(ctx, deviceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrDeviceInvalid
	}
	// Abuse guard: cap events per device per day.
	count, err := s.repo.CountSince(ctx, deviceID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if count+int64(len(batch)) > MaxEventsPerDay {
		return ErrRateLimited
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if len(payload) > MaxBatchSize*MaxPropsBytes {
		return fmt.Errorf("%w: batch too large", ErrInvalidEvent)
	}
	return s.repo.InsertBatch(ctx, deviceID, payload)
}

package persistence

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type EventsRepo struct {
	q *Queries
}

func NewEventsRepo(q *Queries) *EventsRepo {
	return &EventsRepo{q: q}
}

func (r *EventsRepo) InsertBatch(ctx context.Context, deviceID uuid.UUID, payload []byte) error {
	return r.q.InsertEvents(ctx, InsertEventsParams{Column1: deviceID, Column2: payload})
}

func (r *EventsRepo) CountSince(ctx context.Context, deviceID uuid.UUID, since time.Time) (int64, error) {
	return r.q.CountEventsForDeviceSince(ctx, CountEventsForDeviceSinceParams{
		DeviceID:  deviceID,
		CreatedAt: since,
	})
}

func (r *EventsRepo) DeviceExists(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	return r.q.DeviceExistsForAlert(ctx, deviceID)
}

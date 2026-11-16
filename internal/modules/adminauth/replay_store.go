package adminauth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ReplayStore interface {
	// TryUsedTOTP reports whether this counter is fresh (true = accept the
	// code) and records it atomically so concurrent submits cannot both win.
	TryUsedTOTP(ctx context.Context, userID uuid.UUID, step int64) (bool, error)
	// DeleteForUser clears all recorded counters (2FA reset).
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
}

const replayWindow = (2*totpSkew + 1) * totpPeriod * time.Second

// MemoryReplayStore is the fallback when Redis is unavailable.
type MemoryReplayStore struct {
	mu      sync.Mutex
	entries map[uuid.UUID]map[int64]time.Time
}

func NewMemoryReplayStore() *MemoryReplayStore {
	return &MemoryReplayStore{entries: make(map[uuid.UUID]map[int64]time.Time)}
}

func (m *MemoryReplayStore) TryUsedTOTP(_ context.Context, userID uuid.UUID, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.entries[userID]
	if set == nil {
		set = make(map[int64]time.Time)
		m.entries[userID] = set
	}
	now := time.Now()
	for s, at := range set {
		if now.Sub(at) > replayWindow {
			delete(set, s)
		}
	}
	if _, used := set[step]; used {
		return false, nil
	}
	set[step] = now
	return true, nil
}

func (m *MemoryReplayStore) DeleteForUser(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, userID)
	return nil
}

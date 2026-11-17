package adminauth

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefreshEntry is the server-side record for one admin login session,
// keyed by the SHA-256 hash of its current refresh token. Rotating the
// refresh token replaces the key; the entry ID (sid) stays stable so
// access tokens and the sessions UI keep referring to the same session.
type RefreshEntry struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userId"`
	Email     string    `json:"email"`
	UserAgent string    `json:"userAgent"`
	IP        string    `json:"ip"`
	Hash      string    `json:"hash"` // hex sha256 of the current refresh token
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RefreshStore keeps rotating refresh tokens. Consume must be atomic
// (get-and-delete) so a refresh token is strictly single-use: the first
// caller wins, replays and concurrent refreshes see "not found".
type RefreshStore interface {
	Put(ctx context.Context, tokenHash []byte, e *RefreshEntry, ttl time.Duration) error
	Consume(ctx context.Context, tokenHash []byte) (*RefreshEntry, error)
	Get(ctx context.Context, tokenHash []byte) (*RefreshEntry, error)
	Delete(ctx context.Context, tokenHash []byte) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*RefreshEntry, error)
	// DeleteOthers removes every session of userID except the one identified
	// by keepHashHex; empty keepHashHex removes all.
	DeleteOthers(ctx context.Context, userID uuid.UUID, keepHashHex string) error
}

// MemoryRefreshStore is the fallback when Redis is unavailable: sessions
// then live only in this process and are lost on restart.
type MemoryRefreshStore struct {
	mu      sync.Mutex
	entries map[string]*RefreshEntry // hashHex -> entry
	byUser  map[uuid.UUID]map[string]struct{}
}

func NewMemoryRefreshStore() *MemoryRefreshStore {
	return &MemoryRefreshStore{
		entries: make(map[string]*RefreshEntry),
		byUser:  make(map[uuid.UUID]map[string]struct{}),
	}
}

func (m *MemoryRefreshStore) Put(_ context.Context, tokenHash []byte, e *RefreshEntry, _ time.Duration) error {
	h := hex.EncodeToString(tokenHash)
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.entries[h]; old != nil {
		m.removeLocked(old.UserID, h)
	}
	m.entries[h] = e
	set := m.byUser[e.UserID]
	if set == nil {
		set = make(map[string]struct{})
		m.byUser[e.UserID] = set
	}
	set[h] = struct{}{}
	return nil
}

func (m *MemoryRefreshStore) live(h string) *RefreshEntry {
	e := m.entries[h]
	if e == nil || !e.ExpiresAt.After(time.Now()) {
		return nil
	}
	return e
}

func (m *MemoryRefreshStore) Consume(_ context.Context, tokenHash []byte) (*RefreshEntry, error) {
	h := hex.EncodeToString(tokenHash)
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.live(h)
	if e == nil {
		return nil, pgx.ErrNoRows
	}
	delete(m.entries, h)
	m.removeLocked(e.UserID, h)
	return e, nil
}

func (m *MemoryRefreshStore) Get(_ context.Context, tokenHash []byte) (*RefreshEntry, error) {
	h := hex.EncodeToString(tokenHash)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.live(h); e != nil {
		return e, nil
	}
	return nil, pgx.ErrNoRows
}

func (m *MemoryRefreshStore) Delete(_ context.Context, tokenHash []byte) error {
	h := hex.EncodeToString(tokenHash)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.entries[h]; e != nil {
		delete(m.entries, h)
		m.removeLocked(e.UserID, h)
	}
	return nil
}

func (m *MemoryRefreshStore) ListByUser(_ context.Context, userID uuid.UUID) ([]*RefreshEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.byUser[userID]
	out := make([]*RefreshEntry, 0, len(set))
	for h := range set {
		if e := m.live(h); e != nil {
			out = append(out, e)
		} else {
			delete(m.entries, h)
			delete(set, h)
		}
	}
	return out, nil
}

func (m *MemoryRefreshStore) DeleteOthers(_ context.Context, userID uuid.UUID, keepHashHex string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h := range m.byUser[userID] {
		if h != keepHashHex {
			if e := m.entries[h]; e != nil {
				delete(m.entries, h)
			}
			delete(m.byUser[userID], h)
		}
	}
	return nil
}

func (m *MemoryRefreshStore) removeLocked(userID uuid.UUID, h string) {
	if set := m.byUser[userID]; set != nil {
		delete(set, h)
		if len(set) == 0 {
			delete(m.byUser, userID)
		}
	}
}

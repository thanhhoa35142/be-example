package adminauth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PendingChallenge is a login/enroll challenge plus the raw bearer token.
// Token is populated when the challenge is created or reused for the same
// user; token-hash lookups leave it empty.
type PendingChallenge struct {
	Challenge
	Token string
}

// ChallengeStore keeps short-lived login challenges outside Postgres
// (Redis in production, memory for tests/CLI fallback). Challenges are
// single-use and live for Config.ChallengeTTL. Lookups return
// pgx.ErrNoRows when the challenge is missing or expired.
type ChallengeStore interface {
	// Put stores the challenge under its token hash and indexes it by user
	// so a repeat login can reuse a still-valid challenge.
	Put(ctx context.Context, pc *PendingChallenge) error
	// ByTokenHash returns the challenge for a token hash.
	ByTokenHash(ctx context.Context, tokenHash []byte) (*PendingChallenge, error)
	// ForUser returns the user's live challenge (Token populated) so Login
	// can hand back the same challenge instead of minting a new one.
	ForUser(ctx context.Context, userID uuid.UUID) (*PendingChallenge, error)
	// Delete consumes the challenge (both indexes).
	Delete(ctx context.Context, pc *PendingChallenge) error
	// DeleteForUser drops any pending challenge for the user.
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
}

// MemoryChallengeStore is a process-local ChallengeStore. Challenges do not
// survive a restart — used by tests and as a CLI fallback when Redis is
// not configured.
type MemoryChallengeStore struct {
	mu      sync.Mutex
	byToken map[string]*PendingChallenge
	byUser  map[uuid.UUID]string // userID -> token hash hex
	now     func() time.Time
}

func NewMemoryChallengeStore() *MemoryChallengeStore {
	return &MemoryChallengeStore{
		byToken: make(map[string]*PendingChallenge),
		byUser:  make(map[uuid.UUID]string),
		now:     time.Now,
	}
}

func tokenKey(hash []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(hash)*2)
	for i, b := range hash {
		out[i*2] = hexd[b>>4]
		out[i*2+1] = hexd[b&0x0f]
	}
	return string(out)
}

func (m *MemoryChallengeStore) live(pc *PendingChallenge) bool {
	return pc != nil && pc.ExpiresAt.After(m.now())
}

func (m *MemoryChallengeStore) Put(_ context.Context, pc *PendingChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *pc
	cp.TotpSecret = append([]byte(nil), pc.TotpSecret...)
	key := tokenKey(pc.TokenHash)
	if oldKey, ok := m.byUser[pc.AdminUserID]; ok && oldKey != key {
		delete(m.byToken, oldKey)
	}
	m.byToken[key] = &cp
	m.byUser[pc.AdminUserID] = key
	return nil
}

func (m *MemoryChallengeStore) ByTokenHash(_ context.Context, tokenHash []byte) (*PendingChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pc := m.byToken[tokenKey(tokenHash)]
	if !m.live(pc) {
		return nil, pgx.ErrNoRows
	}
	cp := *pc
	return &cp, nil
}

func (m *MemoryChallengeStore) ForUser(_ context.Context, userID uuid.UUID) (*PendingChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byUser[userID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	pc := m.byToken[key]
	if !m.live(pc) {
		delete(m.byToken, key)
		delete(m.byUser, userID)
		return nil, pgx.ErrNoRows
	}
	cp := *pc
	return &cp, nil
}

func (m *MemoryChallengeStore) Delete(_ context.Context, pc *PendingChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tokenKey(pc.TokenHash)
	delete(m.byToken, key)
	if m.byUser[pc.AdminUserID] == key {
		delete(m.byUser, pc.AdminUserID)
	}
	return nil
}

func (m *MemoryChallengeStore) DeleteForUser(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key, ok := m.byUser[userID]; ok {
		delete(m.byToken, key)
		delete(m.byUser, userID)
	}
	return nil
}

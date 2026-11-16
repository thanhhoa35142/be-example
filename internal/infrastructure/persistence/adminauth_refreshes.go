package persistence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

// AdminAuthRefreshStore keeps rotating admin refresh tokens in Redis.
//
//	adminauth:refresh:token:{hex(sha256(token))} -> refreshEntry (TTL)
//	adminauth:refresh:user:{userID}             -> SET of token hash hexes
//
// Consume uses GETDEL so a refresh token is strictly single-use: the first
// caller gets the entry and deletes the key, replays and races get nothing.
// The user index has no TTL of its own; dead members are pruned lazily on
// ListByUser. Token keys expire on their own.
type AdminAuthRefreshStore struct {
	rdb *redis.Client
}

func NewAdminAuthRefreshStore(rdb *redis.Client) *AdminAuthRefreshStore {
	return &AdminAuthRefreshStore{rdb: rdb}
}

const (
	adminRefreshTokenKey = "adminauth:refresh:token:"
	adminRefreshUserKey  = "adminauth:refresh:user:"
)

type adminRefreshPayload struct {
	ID        string `json:"id"`
	UserID    string `json:"u"`
	Email     string `json:"e"`
	UserAgent string `json:"a"`
	IP        string `json:"ip"`
	Hash      string `json:"h"`
	CreatedAt int64  `json:"c"` // unix seconds
	ExpiresAt int64  `json:"x"`
}

func adminRefreshUserSet(userID uuid.UUID) string {
	return adminRefreshUserKey + userID.String()
}

// putRefresh atomically writes the token key and adds its hash to the user
// set. SET ... EX plus SADD are not transactional, but a crash between them
// only leaves a harmless index member pointing at a missing key (pruned on
// read).
func (s *AdminAuthRefreshStore) Put(ctx context.Context, tokenHash []byte, e *adminauth.RefreshEntry, ttl time.Duration) error {
	h := hex.EncodeToString(tokenHash)
	body, err := json.Marshal(adminRefreshPayload{
		ID:        e.ID.String(),
		UserID:    e.UserID.String(),
		Email:     e.Email,
		UserAgent: e.UserAgent,
		IP:        e.IP,
		Hash:      h,
		CreatedAt: e.CreatedAt.Unix(),
		ExpiresAt: e.ExpiresAt.Unix(),
	})
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, adminRefreshTokenKey+h, body, ttl).Err(); err != nil {
		return err
	}
	return s.rdb.SAdd(ctx, adminRefreshUserSet(e.UserID), h).Err()
}

func decodeRefreshEntry(raw []byte) (*adminauth.RefreshEntry, error) {
	var p adminRefreshPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, pgx.ErrNoRows
	}
	uid, err := uuid.Parse(p.UserID)
	if err != nil {
		return nil, pgx.ErrNoRows
	}
	sid, err := uuid.Parse(p.ID)
	if err != nil {
		return nil, pgx.ErrNoRows
	}
	return &adminauth.RefreshEntry{
		ID:        sid,
		UserID:    uid,
		Email:     p.Email,
		UserAgent: p.UserAgent,
		IP:        p.IP,
		Hash:      p.Hash,
		CreatedAt: time.Unix(p.CreatedAt, 0).UTC(),
		ExpiresAt: time.Unix(p.ExpiresAt, 0).UTC(),
	}, nil
}

// Consume atomically reads and deletes the token key (GETDEL, Redis >= 6.2).
// Only the first caller receives the entry.
func (s *AdminAuthRefreshStore) Consume(ctx context.Context, tokenHash []byte) (*adminauth.RefreshEntry, error) {
	h := hex.EncodeToString(tokenHash)
	raw, err := s.rdb.GetDel(ctx, adminRefreshTokenKey+h).Bytes()
	if err != nil {
		if isNotFound(err) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	entry, err := decodeRefreshEntry(raw)
	if err != nil {
		return nil, err
	}
	// Best-effort index cleanup; the token key is already gone either way.
	_ = s.rdb.SRem(ctx, adminRefreshUserSet(entry.UserID), h).Err()
	return entry, nil
}

func (s *AdminAuthRefreshStore) Get(ctx context.Context, tokenHash []byte) (*adminauth.RefreshEntry, error) {
	raw, err := s.rdb.Get(ctx, adminRefreshTokenKey+hex.EncodeToString(tokenHash)).Bytes()
	if err != nil {
		if isNotFound(err) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	return decodeRefreshEntry(raw)
}

func (s *AdminAuthRefreshStore) Delete(ctx context.Context, tokenHash []byte) error {
	h := hex.EncodeToString(tokenHash)
	// Look up first so the user index can be cleaned too.
	if entry, err := s.Get(ctx, tokenHash); err == nil {
		_ = s.rdb.SRem(ctx, adminRefreshUserSet(entry.UserID), h).Err()
	}
	return s.rdb.Del(ctx, adminRefreshTokenKey+h).Err()
}

func (s *AdminAuthRefreshStore) ListByUser(ctx context.Context, userID uuid.UUID) ([]*adminauth.RefreshEntry, error) {
	setKey := adminRefreshUserSet(userID)
	hashes, err := s.rdb.SMembers(ctx, setKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*adminauth.RefreshEntry, 0, len(hashes))
	dead := make([]any, 0)
	for _, h := range hashes {
		raw, err := s.rdb.Get(ctx, adminRefreshTokenKey+h).Bytes()
		if err != nil {
			dead = append(dead, h) // expired or consumed token
			continue
		}
		entry, err := decodeRefreshEntry(raw)
		if err != nil {
			dead = append(dead, h)
			continue
		}
		out = append(out, entry)
	}
	if len(dead) > 0 {
		_ = s.rdb.SRem(ctx, setKey, dead...).Err()
	}
	return out, nil
}

// DeleteOthers removes every refresh session of userID except keepHashHex
// (empty string removes all). Token keys expire on their own; this also
// prunes the user index.
func (s *AdminAuthRefreshStore) DeleteOthers(ctx context.Context, userID uuid.UUID, keepHashHex string) error {
	setKey := adminRefreshUserSet(userID)
	hashes, err := s.rdb.SMembers(ctx, setKey).Result()
	if err != nil {
		return err
	}
	dead := make([]any, 0, len(hashes))
	for _, h := range hashes {
		if h == keepHashHex {
			continue
		}
		dead = append(dead, h)
		_ = s.rdb.Del(ctx, adminRefreshTokenKey+h).Err()
	}
	if len(dead) > 0 {
		_ = s.rdb.SRem(ctx, setKey, dead...).Err()
	}
	return nil
}

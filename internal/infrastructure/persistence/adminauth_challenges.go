package persistence

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

// AdminAuthChallengeStore keeps pending admin login/enroll challenges in
// Redis. Two keys per challenge:
//
//	adminauth:challenge:token:{hex(sha256(token))} -> challengePayload (TTL)
//	adminauth:challenge:user:{userID}             -> userIndex      (TTL)
//
// The user index lets a repeat login reuse a still-valid challenge instead
// of minting a new one; both keys carry the challenge TTL so nothing needs
// a sweeper. Mutations run as Lua scripts so the token key and user index
// can never diverge mid-operation.
type AdminAuthChallengeStore struct {
	rdb *redis.Client
}

func NewAdminAuthChallengeStore(rdb *redis.Client) *AdminAuthChallengeStore {
	return &AdminAuthChallengeStore{rdb: rdb}
}

const (
	adminChallengeTokenKey = "adminauth:challenge:token:"
	adminChallengeUserKey  = "adminauth:challenge:user:"
)

type adminChallengePayload struct {
	UserID     string `json:"u"`
	Kind       string `json:"k"`
	TotpSecret string `json:"s,omitempty"` // base64
	ExpiresAt  int64  `json:"e"`           // unix seconds
}

type adminChallengeUserIndex struct {
	Token     string `json:"t"` // raw bearer token, needed to hand back on reuse
	TokenHash string `json:"h"` // hex sha256(token), to find the token key
	ExpiresAt int64  `json:"e"`
}

func adminChallengeTTL(pc *adminauth.PendingChallenge) time.Duration {
	ttl := time.Until(pc.ExpiresAt)
	if ttl < time.Second {
		return time.Second
	}
	return ttl
}

// putChallenge atomically writes the token key, replaces the user index, and
// drops the previous challenge's token key when the index moves to a new hash.
var putChallengeScript = redis.NewScript(`
local old = redis.call('GET', KEYS[2])
if old then
  local ok, idx = pcall(cjson.decode, old)
  if ok and idx['h'] and idx['h'] ~= ARGV[4] then
    redis.call('DEL', ARGV[5] .. idx['h'])
  end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', tonumber(ARGV[3]))
redis.call('SET', KEYS[2], ARGV[2], 'PX', tonumber(ARGV[3]))
return 1
`)

// deleteChallenge atomically removes the token key and clears the user index
// only when it still points at this challenge's hash.
var deleteChallengeScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
local old = redis.call('GET', KEYS[2])
if old then
  local ok, idx = pcall(cjson.decode, old)
  if ok and idx['h'] == ARGV[1] then
    redis.call('DEL', KEYS[2])
  end
end
return 1
`)

// deleteForUser atomically removes the user index and the token key it points to.
var deleteForUserScript = redis.NewScript(`
local old = redis.call('GET', KEYS[1])
if old then
  local ok, idx = pcall(cjson.decode, old)
  if ok and idx['h'] then
    redis.call('DEL', ARGV[1] .. idx['h'])
  end
end
redis.call('DEL', KEYS[1])
return 1
`)

func (s *AdminAuthChallengeStore) Put(ctx context.Context, pc *adminauth.PendingChallenge) error {
	ttl := adminChallengeTTL(pc)
	hashHex := hex.EncodeToString(pc.TokenHash)

	payload := adminChallengePayload{
		UserID:    pc.AdminUserID.String(),
		Kind:      pc.Kind,
		ExpiresAt: pc.ExpiresAt.Unix(),
	}
	if len(pc.TotpSecret) > 0 {
		payload.TotpSecret = base64.StdEncoding.EncodeToString(pc.TotpSecret)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	idxBody, err := json.Marshal(adminChallengeUserIndex{
		Token:     pc.Token,
		TokenHash: hashHex,
		ExpiresAt: pc.ExpiresAt.Unix(),
	})
	if err != nil {
		return err
	}
	return putChallengeScript.Run(ctx, s.rdb,
		[]string{
			adminChallengeTokenKey + hashHex,
			adminChallengeUserKey + pc.AdminUserID.String(),
		},
		string(body), string(idxBody), ttl.Milliseconds(), hashHex, adminChallengeTokenKey,
	).Err()
}

func (s *AdminAuthChallengeStore) payloadToChallenge(hash []byte, p *adminChallengePayload) (*adminauth.PendingChallenge, error) {
	uid, err := uuid.Parse(p.UserID)
	if err != nil {
		return nil, pgx.ErrNoRows
	}
	pc := &adminauth.PendingChallenge{
		Challenge: adminauth.Challenge{
			TokenHash:   hash,
			AdminUserID: uid,
			Kind:        p.Kind,
			ExpiresAt:   time.Unix(p.ExpiresAt, 0).UTC(),
		},
	}
	if p.TotpSecret != "" {
		pc.TotpSecret, err = base64.StdEncoding.DecodeString(p.TotpSecret)
		if err != nil {
			return nil, pgx.ErrNoRows
		}
	}
	return pc, nil
}

// isNotFound reports whether err means "no such key" rather than a Redis
// outage — only redis.Nil maps to pgx.ErrNoRows.
func isNotFound(err error) bool {
	return errors.Is(err, redis.Nil) || errors.Is(err, pgx.ErrNoRows)
}

func (s *AdminAuthChallengeStore) ByTokenHash(ctx context.Context, tokenHash []byte) (*adminauth.PendingChallenge, error) {
	raw, err := s.rdb.Get(ctx, adminChallengeTokenKey+hex.EncodeToString(tokenHash)).Bytes()
	if err != nil {
		if isNotFound(err) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	var p adminChallengePayload
	if json.Unmarshal(raw, &p) != nil {
		return nil, pgx.ErrNoRows
	}
	pc, err := s.payloadToChallenge(tokenHash, &p)
	if err != nil {
		return nil, err
	}
	if !pc.ExpiresAt.After(time.Now()) {
		_ = s.Delete(ctx, pc)
		return nil, pgx.ErrNoRows
	}
	return pc, nil
}

func (s *AdminAuthChallengeStore) ForUser(ctx context.Context, userID uuid.UUID) (*adminauth.PendingChallenge, error) {
	userKey := adminChallengeUserKey + userID.String()
	raw, err := s.rdb.Get(ctx, userKey).Bytes()
	if err != nil {
		if isNotFound(err) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	var idx adminChallengeUserIndex
	if json.Unmarshal(raw, &idx) != nil || idx.TokenHash == "" {
		return nil, pgx.ErrNoRows
	}
	hash, err := hex.DecodeString(idx.TokenHash)
	if err != nil {
		return nil, pgx.ErrNoRows
	}
	pc, err := s.ByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = s.rdb.Del(ctx, userKey).Err()
		}
		return nil, err
	}
	pc.Token = idx.Token
	return pc, nil
}

func (s *AdminAuthChallengeStore) Delete(ctx context.Context, pc *adminauth.PendingChallenge) error {
	return deleteChallengeScript.Run(ctx, s.rdb,
		[]string{
			adminChallengeTokenKey + hex.EncodeToString(pc.TokenHash),
			adminChallengeUserKey + pc.AdminUserID.String(),
		},
		hex.EncodeToString(pc.TokenHash),
	).Err()
}

func (s *AdminAuthChallengeStore) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	return deleteForUserScript.Run(ctx, s.rdb,
		[]string{adminChallengeUserKey + userID.String()},
		adminChallengeTokenKey,
	).Err()
}

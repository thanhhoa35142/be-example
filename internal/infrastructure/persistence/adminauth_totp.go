package persistence

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

// AdminAuthReplayStore keeps accepted TOTP counters in Redis.
//
//	adminauth:totp:{userID} -> SET of step numbers (TTL ~2min, refreshed on
//	                           every accepted code)
//
// TryUsedTOTP is a single Lua script: SADD is the atomic replay check and
// EXPIRE keeps the set alive only while codes are being submitted.
type AdminAuthReplayStore struct {
	rdb *redis.Client
}

func NewAdminAuthReplayStore(rdb *redis.Client) *AdminAuthReplayStore {
	return &AdminAuthReplayStore{rdb: rdb}
}

var _ adminauth.ReplayStore = (*AdminAuthReplayStore)(nil)

const adminTOTPKey = "adminauth:totp:"

// tryUsedScript returns 1 when the step was newly added (fresh code) and 0
// when it was already present (replay). The key TTL slides with activity.
var tryUsedScript = redis.NewScript(`
local added = redis.call('SADD', KEYS[1], ARGV[1])
if added == 1 then
  redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
end
return added
`)

func (s *AdminAuthReplayStore) TryUsedTOTP(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	res, err := tryUsedScript.Run(ctx, s.rdb,
		[]string{adminTOTPKey + userID.String()},
		strconv.FormatInt(step, 10),
		int((90 * time.Second).Seconds()),
	).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (s *AdminAuthReplayStore) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	return s.rdb.Del(ctx, adminTOTPKey+userID.String()).Err()
}

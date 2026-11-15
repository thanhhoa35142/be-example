package persistence

import (
	"context"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

func TestAdminAuthChallengeStoreRedis(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("REDIS_TEST_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}

	s := NewAdminAuthChallengeStore(rdb)
	uid := uuid.New()
	token := "test-token-abc"
	hash := adminauth.HashToken(token)
	pc := &adminauth.PendingChallenge{
		Challenge: adminauth.Challenge{
			TokenHash: hash, AdminUserID: uid, Kind: adminauth.ChallengeLogin,
			ExpiresAt: time.Now().Add(2 * time.Minute),
		},
		Token: token,
	}
	// uid + token are unique per run; clean both keys up afterwards.
	t.Cleanup(func() {
		_ = rdb.Del(ctx, adminChallengeTokenKey+hex.EncodeToString(hash)).Err()
		_ = rdb.Del(ctx, adminChallengeUserKey+uid.String()).Err()
	})

	if err := s.Put(ctx, pc); err != nil {
		t.Fatal(err)
	}
	got, err := s.ByTokenHash(ctx, hash)
	if err != nil || got.Kind != adminauth.ChallengeLogin {
		t.Fatalf("bytoken: %v %+v", err, got)
	}
	forUser, err := s.ForUser(ctx, uid)
	if err != nil || forUser.Token != token {
		t.Fatalf("foruser: %v %+v", err, forUser)
	}
	if err := s.Delete(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByTokenHash(ctx, hash); err != pgx.ErrNoRows {
		t.Fatalf("expected consumed, got %v", err)
	}
	if _, err := s.ForUser(ctx, uid); err != pgx.ErrNoRows {
		t.Fatalf("expected user index gone, got %v", err)
	}
	// DeleteForUser path
	if err := s.Put(ctx, pc); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteForUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByTokenHash(ctx, hash); err != pgx.ErrNoRows {
		t.Fatalf("expected gone after DeleteForUser, got %v", err)
	}
}

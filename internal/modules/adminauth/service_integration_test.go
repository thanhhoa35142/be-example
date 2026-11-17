package adminauth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/platform/testdb"
)

func newService(t *testing.T) (*adminauth.Service, *pgxpool.Pool) {
	t.Helper()
	dsn := testdb.DSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}
	svc, err := adminauth.NewService(persistence.NewAdminAuthRepo(persistence.New(pool)), adminauth.NewMemoryChallengeStore(), adminauth.NewMemoryRefreshStore(), adminauth.NewMemoryReplayStore(), key, nil, adminauth.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return svc, pool
}

func cleanupUser(t *testing.T, pool *pgxpool.Pool, email string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM t_admin_users WHERE email = $1`, email)
	})
}

func createUser(t *testing.T, svc *adminauth.Service, pool *pgxpool.Pool, email, password string) {
	t.Helper()
	cleanupUser(t, pool, email)
	if _, err := svc.CreateUser(context.Background(), email, password); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

func totpCode(t *testing.T, secret string) string {
	t.Helper()
	return totpCodeAt(t, secret, time.Now())
}

func totpCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func mustAuthenticate(t *testing.T, svc *adminauth.Service, access string) {
	t.Helper()
	if _, _, err := svc.Authenticate(context.Background(), access); err != nil {
		t.Fatalf("authenticate access token: %v", err)
	}
}

func TestAdminAuthFlow(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	email := fmt.Sprintf("test-%d@example.com", time.Now().UnixNano())
	createUser(t, svc, pool, email, "a-very-long-test-password")

	if _, err := svc.Login(ctx, "nobody@example.com", "whatever-password", "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("unknown email: %v", err)
	}
	if _, err := svc.Login(ctx, email, "wrong-password-xx", "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}

	login, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Mode != "enroll" || login.EnrollSecret == "" || login.Challenge == "" {
		t.Fatalf("expected enroll result, got %+v", login)
	}

	// A repeat login while the challenge is still valid reuses it: same
	// token, same pending secret — no pile-up of challenges.
	again, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("login reuse: %v", err)
	}
	if again.Challenge != login.Challenge || again.EnrollSecret != login.EnrollSecret {
		t.Fatalf("enroll challenge not reused: %+v vs %+v", again, login)
	}

	if _, _, err := svc.ConfirmEnroll(ctx, login.Challenge, "000000", "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("bad enroll code: %v", err)
	}
	good := totpCode(t, login.EnrollSecret)
	res, pair, err := svc.ConfirmEnroll(ctx, login.Challenge, good, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if len(res.RecoveryCodes) != 10 {
		t.Fatalf("want 10 recovery codes, got %d", len(res.RecoveryCodes))
	}
	if pair == nil || pair.Access == "" || pair.Refresh == "" {
		t.Fatal("no token pair issued")
	}

	mustAuthenticate(t, svc, pair.Access)
	if err := svc.Logout(ctx, pair.Refresh); err != nil {
		t.Fatal(err)
	}
	// Access JWT is stateless and survives logout until its TTL; the refresh
	// token is what actually ends the session.
	if _, _, err := svc.RefreshSession(ctx, pair.Refresh, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("refresh survived logout: %v", err)
	}

	login2, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("login2: %v", err)
	}
	if login2.Mode != "totp" {
		t.Fatalf("expected totp mode, got %s", login2.Mode)
	}
	again2, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("login2 reuse: %v", err)
	}
	if again2.Challenge != login2.Challenge || again2.Mode != "totp" {
		t.Fatalf("totp challenge not reused: %+v vs %+v", again2, login2)
	}

	code := totpCodeAt(t, login.EnrollSecret, time.Now().Add(30*time.Second))
	if _, _, err := svc.ConfirmCode(ctx, login2.Challenge, code, "ua", "1.1.1.1"); err != nil {
		t.Fatalf("totp confirm: %v", err)
	}
	login3, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ConfirmCode(ctx, login3.Challenge, code, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("replayed TOTP accepted: %v", err)
	}

	login4, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	rc := res.RecoveryCodes[0]
	_, pair2, err := svc.ConfirmCode(ctx, login4.Challenge, rc, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	mustAuthenticate(t, svc, pair2.Access)
	login5, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ConfirmCode(ctx, login5.Challenge, rc, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("reused recovery code accepted: %v", err)
	}

	items, err := svc.Sessions(ctx, pair2.Refresh)
	if err != nil || len(items) == 0 {
		t.Fatalf("sessions: %v %d", err, len(items))
	}
	if err := svc.RevokeOtherSessions(ctx, pair2.Refresh); err != nil {
		t.Fatal(err)
	}

	fresh, err := svc.RegenerateRecoveryCodes(ctx, pair2.Access)
	if err != nil || len(fresh) != 10 {
		t.Fatalf("regenerate: %v", err)
	}

	if err := svc.SetDisabled(ctx, email, true); err != nil {
		t.Fatal(err)
	}
	// Disabled users fail on both paths: access JWT (DisabledAt + epoch) and refresh.
	if _, _, err := svc.Authenticate(ctx, pair2.Access); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("disabled access token alive: %v", err)
	}
	if _, _, err := svc.RefreshSession(ctx, pair2.Refresh, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("disabled refresh token alive: %v", err)
	}
	if _, err := svc.Login(ctx, email, "a-very-long-test-password", "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("disabled login: %v", err)
	}
	if err := svc.SetDisabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
}

func TestAdminAuthRefreshRotation(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	email := fmt.Sprintf("rot-%d@example.com", time.Now().UnixNano())
	createUser(t, svc, pool, email, "a-very-long-test-password")

	login, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	_, pair, err := svc.ConfirmEnroll(ctx, login.Challenge, totpCode(t, login.EnrollSecret), "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}

	// Rotate: old refresh dies, new pair works.
	pair2, user, err := svc.RefreshSession(ctx, pair.Refresh, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if user.Email != email {
		t.Fatalf("refresh user: %v", user.Email)
	}
	mustAuthenticate(t, svc, pair2.Access)

	// Replay of the consumed refresh token must fail (single-use).
	if _, _, err := svc.RefreshSession(ctx, pair.Refresh, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("replayed refresh accepted: %v", err)
	}

	// Second rotation keeps the same session id.
	pair3, _, err := svc.RefreshSession(ctx, pair2.Refresh, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	sess2, _, _ := svc.Authenticate(ctx, pair2.Access)
	sess3, _, _ := svc.Authenticate(ctx, pair3.Access)
	if sess2.ID != sess3.ID {
		t.Fatalf("session id changed across rotations: %s vs %s", sess2.ID, sess3.ID)
	}
}

func TestAdminAuthLockout(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	email := fmt.Sprintf("lock-%d@example.com", time.Now().UnixNano())
	createUser(t, svc, pool, email, "a-very-long-test-password")

	for i := 0; i < 5; i++ {
		if _, err := svc.Login(ctx, email, "wrong-password-xx", "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, err := svc.Login(ctx, email, "a-very-long-test-password", "", "1.1.1.1")
	var locked *adminauth.LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("expected LockedError, got %v", err)
	}
	if locked.RetryAfter <= 0 || locked.RetryAfter > 15*time.Minute {
		t.Fatalf("bad retryAfter %v", locked.RetryAfter)
	}
}

func TestAdminAuthStaffOps(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	email := fmt.Sprintf("staff-%d@example.com", time.Now().UnixNano())
	createUser(t, svc, pool, email, "a-very-long-test-password")

	if _, err := svc.CreateUser(ctx, email, "another-long-password"); !errors.Is(err, adminauth.ErrUserExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, err := svc.CreateUser(ctx, "short@example.com", "short"); err == nil {
		t.Fatal("short password accepted")
	}

	login, err := svc.Login(ctx, email, "a-very-long-test-password", "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ConfirmEnroll(ctx, login.Challenge, totpCode(t, login.EnrollSecret), "", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reset2FA(ctx, email); err != nil {
		t.Fatal(err)
	}
	login2, err := svc.Login(ctx, email, "a-very-long-test-password", "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if login2.Mode != "enroll" {
		t.Fatalf("after reset-2fa expected enroll, got %s", login2.Mode)
	}
}

func TestAdminAuthChangePassword(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	email := fmt.Sprintf("pw-%d@example.com", time.Now().UnixNano())
	const oldPw, newPw = "a-very-long-test-password", "another-long-test-password"
	createUser(t, svc, pool, email, oldPw)

	login, err := svc.Login(ctx, email, oldPw, "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	_, mine, err := svc.ConfirmEnroll(ctx, login.Challenge, totpCode(t, login.EnrollSecret), "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}

	login2, err := svc.Login(ctx, email, oldPw, "", "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := svc.ConfirmCode(ctx, login2.Challenge, totpCodeAt(t, login.EnrollSecret, time.Now().Add(30*time.Second)), "", "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangePassword(ctx, mine.Access, mine.Refresh, "not-the-password", newPw); !errors.Is(err, adminauth.ErrWrongPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := svc.ChangePassword(ctx, mine.Access, mine.Refresh, oldPw, "short"); !errors.Is(err, adminauth.ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	if err := svc.ChangePassword(ctx, mine.Access, mine.Refresh, oldPw, oldPw); !errors.Is(err, adminauth.ErrPasswordUnchanged) {
		t.Fatalf("unchanged password: %v", err)
	}
	if err := svc.ChangePassword(ctx, "no-such-session", "", oldPw, newPw); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("no session: %v", err)
	}

	if err := svc.ChangePassword(ctx, mine.Access, mine.Refresh, oldPw, newPw); err != nil {
		t.Fatalf("change password: %v", err)
	}
	// Password change bumps auth_epoch: every outstanding access JWT dies,
	// including this session's — the client refreshes transparently.
	if _, _, err := svc.Authenticate(ctx, mine.Access); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("stale-epoch access token survived change: %v", err)
	}
	// The current session's refresh token was kept, so it can rotate into a
	// fresh pair; the other session's refresh token was revoked.
	mine2, _, err := svc.RefreshSession(ctx, mine.Refresh, "", "1.1.1.1")
	if err != nil {
		t.Fatalf("own refresh after change: %v", err)
	}
	mustAuthenticate(t, svc, mine2.Access)
	if _, _, err := svc.RefreshSession(ctx, other.Refresh, "", "2.2.2.2"); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("other session survived the change: %v", err)
	}
	if _, err := svc.Login(ctx, email, oldPw, "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("old password still signs in: %v", err)
	}
	if res, err := svc.Login(ctx, email, newPw, "", "1.1.1.1"); err != nil || res.Mode != "totp" {
		t.Fatalf("new password login: %v %+v", err, res)
	}

	if err := svc.SetPassword(ctx, email, "short"); !errors.Is(err, adminauth.ErrWeakPassword) {
		t.Fatalf("cli weak password: %v", err)
	}
	if err := svc.SetPassword(ctx, email, oldPw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, mine2.Access); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("stale-epoch access token survived set-password: %v", err)
	}
	if _, _, err := svc.RefreshSession(ctx, mine2.Refresh, "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("refresh survived set-password: %v", err)
	}
	if res, err := svc.Login(ctx, email, oldPw, "", "1.1.1.1"); err != nil || res.Mode != "totp" {
		t.Fatalf("login after set-password: %v %+v", err, res)
	}
}

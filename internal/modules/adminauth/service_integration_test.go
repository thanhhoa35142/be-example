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
	svc, err := adminauth.NewService(persistence.NewAdminAuthRepo(persistence.New(pool)), key, adminauth.DefaultConfig())
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

	if _, _, _, err := svc.ConfirmEnroll(ctx, login.Challenge, "000000", "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("bad enroll code: %v", err)
	}
	good := totpCode(t, login.EnrollSecret)
	res, sess, raw, err := svc.ConfirmEnroll(ctx, login.Challenge, good, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if len(res.RecoveryCodes) != 10 {
		t.Fatalf("want 10 recovery codes, got %d", len(res.RecoveryCodes))
	}
	if sess == nil || raw == "" {
		t.Fatal("no session issued")
	}

	if _, u, err := svc.Authenticate(ctx, raw); err != nil || u.Email != email {
		t.Fatalf("authenticate: %v", err)
	}
	if err := svc.Logout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, raw); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("session survived logout: %v", err)
	}

	login2, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("login2: %v", err)
	}
	if login2.Mode != "totp" {
		t.Fatalf("expected totp mode, got %s", login2.Mode)
	}

	code := totpCodeAt(t, login.EnrollSecret, time.Now().Add(30*time.Second))
	if _, _, _, err := svc.ConfirmCode(ctx, login2.Challenge, code, "ua", "1.1.1.1"); err != nil {
		t.Fatalf("totp confirm: %v", err)
	}
	login3, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ConfirmCode(ctx, login3.Challenge, code, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("replayed TOTP accepted: %v", err)
	}

	login4, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	rc := res.RecoveryCodes[0]
	_, _, raw2, err := svc.ConfirmCode(ctx, login4.Challenge, rc, "ua", "1.1.1.1")
	if err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, raw2); err != nil {
		t.Fatalf("recovery session: %v", err)
	}
	login5, err := svc.Login(ctx, email, "a-very-long-test-password", "ua", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ConfirmCode(ctx, login5.Challenge, rc, "ua", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCode) {
		t.Fatalf("reused recovery code accepted: %v", err)
	}

	items, err := svc.Sessions(ctx, raw2)
	if err != nil || len(items) == 0 {
		t.Fatalf("sessions: %v %d", err, len(items))
	}
	if err := svc.RevokeOtherSessions(ctx, raw2); err != nil {
		t.Fatal(err)
	}

	fresh, err := svc.RegenerateRecoveryCodes(ctx, raw2)
	if err != nil || len(fresh) != 10 {
		t.Fatalf("regenerate: %v", err)
	}

	if err := svc.SetDisabled(ctx, email, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, raw2); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("disabled session alive: %v", err)
	}
	if _, err := svc.Login(ctx, email, "a-very-long-test-password", "", "1.1.1.1"); !errors.Is(err, adminauth.ErrInvalidCredentials) {
		t.Fatalf("disabled login: %v", err)
	}
	if err := svc.SetDisabled(ctx, email, false); err != nil {
		t.Fatal(err)
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
	if _, _, _, err := svc.ConfirmEnroll(ctx, login.Challenge, totpCode(t, login.EnrollSecret), "", "1.1.1.1"); err != nil {
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
	_, _, mine, err := svc.ConfirmEnroll(ctx, login.Challenge, totpCode(t, login.EnrollSecret), "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}

	login2, err := svc.Login(ctx, email, oldPw, "", "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	_, _, other, err := svc.ConfirmCode(ctx, login2.Challenge, totpCodeAt(t, login.EnrollSecret, time.Now().Add(30*time.Second)), "", "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangePassword(ctx, mine, "not-the-password", newPw); !errors.Is(err, adminauth.ErrWrongPassword) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := svc.ChangePassword(ctx, mine, oldPw, "short"); !errors.Is(err, adminauth.ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	if err := svc.ChangePassword(ctx, mine, oldPw, oldPw); !errors.Is(err, adminauth.ErrPasswordUnchanged) {
		t.Fatalf("unchanged password: %v", err)
	}
	if err := svc.ChangePassword(ctx, "no-such-session", oldPw, newPw); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("no session: %v", err)
	}

	if err := svc.ChangePassword(ctx, mine, oldPw, newPw); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, mine); err != nil {
		t.Fatalf("own session lost after change: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, other); !errors.Is(err, adminauth.ErrInvalidSession) {
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
	if _, _, err := svc.Authenticate(ctx, mine); !errors.Is(err, adminauth.ErrInvalidSession) {
		t.Fatalf("session survived set-password: %v", err)
	}
	if res, err := svc.Login(ctx, email, oldPw, "", "1.1.1.1"); err != nil || res.Mode != "totp" {
		t.Fatalf("login after set-password: %v %+v", err, res)
	}
}

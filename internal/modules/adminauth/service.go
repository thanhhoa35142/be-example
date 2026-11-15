package adminauth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LoginResult struct {
	Challenge    string `json:"challenge"`
	Mode         string `json:"mode"`
	EnrollSecret string `json:"enrollSecret,omitempty"`
	OTPAuthURL   string `json:"otpauthUrl,omitempty"`
}

type AuthResult struct {
	User          UserInfo `json:"user"`
	RecoveryCodes []string `json:"recoveryCodes,omitempty"`
}

type UserInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string { return ErrAccountLocked.Error() }
func (e *LockedError) Unwrap() error { return ErrAccountLocked }

func IsLocked(err error) bool {
	var le *LockedError
	return errors.As(err, &le)
}

func (e *LockedError) RetryAfterDuration() time.Duration { return e.RetryAfter }

type Service struct {
	repo      Repository
	cipher    *Cipher
	issuer    string
	cfg       Config
	now       func() time.Time
	dummyHash string
}

func NewService(repo Repository, key []byte, cfg Config) (*Service, error) {
	var c *Cipher
	if len(key) > 0 {
		var err error
		c, err = NewCipher(key)
		if err != nil {
			return nil, err
		}
	}
	dummy, err := HashPassword("adminauth-dummy-" + time.Now().String())
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, cipher: c, issuer: "Pickhour", cfg: cfg, now: time.Now, dummyHash: dummy}, nil
}

func (s *Service) SessionTTL() time.Duration { return s.cfg.SessionTTL }

var ErrKeyMissing = errors.New("admin auth key not configured")

func (s *Service) requireCipher() (*Cipher, error) {
	if s.cipher == nil {
		return nil, ErrKeyMissing
	}
	return s.cipher, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func info(u *User) UserInfo {
	return UserInfo{ID: u.ID.String(), Email: u.Email}
}

func isNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func (s *Service) Login(ctx context.Context, email, password, userAgent, ip string) (LoginResult, error) {
	email = normalizeEmail(email)
	user, err := s.repo.UserByEmail(ctx, email)
	if err != nil && !isNotFound(err) {
		return LoginResult{}, err
	}
	if user == nil {

		_ = VerifyPassword(s.dummyHash, password)
		return LoginResult{}, ErrInvalidCredentials
	}
	if !VerifyPassword(user.PasswordHash, password) {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return LoginResult{}, ErrInvalidCredentials
	}
	if user.DisabledAt != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.now()) {
		return LoginResult{}, &LockedError{RetryAfter: user.LockedUntil.Sub(s.now())}
	}

	token, hash, err := NewChallengeToken()
	if err != nil {
		return LoginResult{}, err
	}
	expires := s.now().Add(s.cfg.ChallengeTTL)

	if user.TotpConfirmedAt == nil || len(user.TotpSecret) == 0 {
		cipher, err := s.requireCipher()
		if err != nil {
			return LoginResult{}, err
		}
		secret, url, err := NewTOTPSecret(s.issuer, email)
		if err != nil {
			return LoginResult{}, err
		}
		sealed, err := cipher.Seal([]byte(secret))
		if err != nil {
			return LoginResult{}, err
		}
		if err := s.repo.CreateChallenge(ctx, Challenge{
			TokenHash: hash, AdminUserID: user.ID, Kind: ChallengeEnroll,
			TotpSecret: sealed, ExpiresAt: expires,
		}); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Challenge: token, Mode: "enroll", EnrollSecret: secret, OTPAuthURL: url}, nil
	}

	if err := s.repo.CreateChallenge(ctx, Challenge{
		TokenHash: hash, AdminUserID: user.ID, Kind: ChallengeLogin, ExpiresAt: expires,
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Challenge: token, Mode: "totp"}, nil
}

func (s *Service) challengeUser(ctx context.Context, token string, wantKind string) (*Challenge, *User, error) {
	ch, err := s.repo.ChallengeByToken(ctx, HashToken(token))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidChallenge
		}
		return nil, nil, err
	}
	if ch.Kind != wantKind || !ch.ExpiresAt.After(s.now()) {
		_ = s.repo.DeleteChallenge(ctx, ch.TokenHash)
		return nil, nil, ErrInvalidChallenge
	}
	user, err := s.repo.UserByID(ctx, ch.AdminUserID)
	if err != nil {
		return nil, nil, err
	}
	if user.DisabledAt != nil {
		return nil, nil, ErrInvalidCredentials
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.now()) {
		return nil, nil, &LockedError{RetryAfter: user.LockedUntil.Sub(s.now())}
	}
	return ch, user, nil
}

func (s *Service) verifyTOTPStep(ctx context.Context, userID uuid.UUID, secretB32, code string) bool {
	counter, ok := ValidateTOTP(secretB32, code, s.now())
	if !ok {
		return false
	}
	fresh, err := s.repo.TryUsedTOTP(ctx, userID, counter)
	if err != nil || !fresh {
		return false
	}
	_ = s.repo.PruneTOTPUsed(ctx)
	return true
}

func (s *Service) ConfirmCode(ctx context.Context, challenge, code, userAgent, ip string) (*AuthResult, *Session, string, error) {
	_, user, err := s.challengeUser(ctx, challenge, ChallengeLogin)
	if err != nil {
		return nil, nil, "", err
	}

	code = strings.TrimSpace(code)
	var ok bool
	if len(code) == totpDigits {
		secret, err := s.openSecret(user.TotpSecret)
		if err != nil {
			return nil, nil, "", err
		}
		ok = s.verifyTOTPStep(ctx, user.ID, secret, code)
	} else {
		ok, err = s.repo.ConsumeRecoveryCode(ctx, user.ID, HashRecoveryCode(code))
		if err != nil {
			return nil, nil, "", err
		}
	}
	if !ok {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return nil, nil, "", ErrInvalidCode
	}

	sess, raw, err := s.openSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, nil, "", err
	}
	return &AuthResult{User: info(user)}, sess, raw, nil
}

func (s *Service) ConfirmEnroll(ctx context.Context, challenge, code, userAgent, ip string) (*AuthResult, *Session, string, error) {
	ch, user, err := s.challengeUser(ctx, challenge, ChallengeEnroll)
	if err != nil {
		return nil, nil, "", err
	}
	cipher, err := s.requireCipher()
	if err != nil {
		return nil, nil, "", err
	}
	secret, err := cipher.Open(ch.TotpSecret)
	if err != nil {
		return nil, nil, "", err
	}
	if !s.verifyTOTPStep(ctx, user.ID, string(secret), strings.TrimSpace(code)) {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return nil, nil, "", ErrInvalidCode
	}

	codes, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, nil, "", err
	}
	if err := s.repo.ConfirmTOTP(ctx, user.ID, ch.TotpSecret); err != nil {
		return nil, nil, "", err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, user.ID, hashes); err != nil {
		return nil, nil, "", err
	}
	_ = s.repo.DeleteChallenge(ctx, ch.TokenHash)
	_ = s.repo.ResetAuthFailures(ctx, user.ID)

	sess, raw, err := s.openSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, nil, "", err
	}
	return &AuthResult{User: info(user), RecoveryCodes: codes}, sess, raw, nil
}

func (s *Service) openSession(ctx context.Context, user *User, userAgent, ip string) (*Session, string, error) {
	token, hash, err := NewSessionToken()
	if err != nil {
		return nil, "", err
	}
	sess, err := s.repo.CreateSession(ctx, hash, user.ID, userAgent, ip, s.now().Add(s.cfg.SessionTTL))
	if err != nil {
		return nil, "", err
	}
	_ = s.repo.TouchLastLogin(ctx, user.ID)
	_ = s.repo.ResetAuthFailures(ctx, user.ID)
	return sess, token, nil
}

func (s *Service) openSecret(sealed []byte) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	plain, err := cipher.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (*Session, *User, error) {
	if token == "" {
		return nil, nil, ErrInvalidSession
	}
	sess, user, err := s.repo.SessionByToken(ctx, HashToken(token))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidSession
		}
		return nil, nil, err
	}
	now := s.now()
	if user.DisabledAt != nil || !sess.ExpiresAt.After(now) || !sess.LastSeenAt.Add(s.cfg.IdleTTL).After(now) {
		return nil, nil, ErrInvalidSession
	}
	_ = s.repo.TouchSession(ctx, sess.ID)
	return sess, user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, HashToken(token))
}

func (s *Service) Me(ctx context.Context, token string) (*Session, *User, error) {
	return s.Authenticate(ctx, token)
}

func (s *Service) Sessions(ctx context.Context, token string) ([]SessionInfo, error) {
	sess, user, err := s.Authenticate(ctx, token)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListSessions(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionInfo{
			ID: r.ID, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
			LastSeenAt: r.LastSeenAt, UserAgent: r.UserAgent, IP: r.IP,
			Current: bytes.Equal(r.TokenHash, sess.TokenHash),
		})
	}
	return out, nil
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, token string) ([]string, error) {
	_, user, err := s.Authenticate(ctx, token)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, user.ID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, token string) error {
	sess, _, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	return s.repo.RevokeOtherSessions(ctx, sess.UserID, sess.TokenHash)
}

func (s *Service) ChangePassword(ctx context.Context, token, current, next string) error {
	sess, sessUser, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}

	user, err := s.repo.UserByID(ctx, sessUser.ID)
	if err != nil {
		return err
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.now()) {
		return &LockedError{RetryAfter: user.LockedUntil.Sub(s.now())}
	}
	if !VerifyPassword(user.PasswordHash, current) {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return ErrWrongPassword
	}
	if len(next) < MinPasswordLength {
		return ErrWeakPassword
	}
	if next == current {
		return ErrPasswordUnchanged
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, user.ID, hash); err != nil {
		return err
	}
	if err := s.repo.RevokeOtherSessions(ctx, user.ID, sess.TokenHash); err != nil {
		return err
	}
	return s.repo.DeleteChallengesForUser(ctx, user.ID)
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.repo.ListUsers(ctx)
}

func (s *Service) CreateUser(ctx context.Context, email, password string) (*User, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, ErrInvalidCredentials
	}
	if len(password) < MinPasswordLength {
		return nil, ErrWeakPassword
	}
	if existing, err := s.repo.UserByEmail(ctx, email); err == nil && existing != nil {
		return nil, ErrUserExists
	} else if err != nil && !isNotFound(err) {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateUser(ctx, email, hash)
}

func (s *Service) SetPassword(ctx context.Context, email, password string) error {
	user, err := s.userByEmail(ctx, email)
	if err != nil {
		return err
	}
	if len(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, user.ID, hash); err != nil {
		return err
	}
	if err := s.repo.DeleteChallengesForUser(ctx, user.ID); err != nil {
		return err
	}
	return s.repo.RevokeOtherSessions(ctx, user.ID, nil)
}

func (s *Service) Reset2FA(ctx context.Context, email string) error {
	user, err := s.userByEmail(ctx, email)
	if err != nil {
		return err
	}
	if err := s.repo.ResetTOTP(ctx, user.ID); err != nil {
		return err
	}
	if err := s.repo.DeleteChallengesForUser(ctx, user.ID); err != nil {
		return err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, user.ID, nil); err != nil {
		return err
	}
	return s.repo.DeleteTOTPUsed(ctx, user.ID)
}

func (s *Service) SetDisabled(ctx context.Context, email string, disabled bool) error {
	user, err := s.userByEmail(ctx, email)
	if err != nil {
		return err
	}
	if err := s.repo.SetDisabled(ctx, user.ID, disabled); err != nil {
		return err
	}
	if disabled {

		return s.repo.RevokeOtherSessions(ctx, user.ID, nil)
	}
	return nil
}

func (s *Service) userByEmail(ctx context.Context, email string) (*User, error) {
	user, err := s.repo.UserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if isNotFound(err) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}

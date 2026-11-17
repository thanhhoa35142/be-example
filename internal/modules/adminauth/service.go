package adminauth

import (
	"context"
	"encoding/hex"
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
	repo       Repository
	challenges ChallengeStore
	refreshes  RefreshStore
	replays    ReplayStore
	cipher     *Cipher
	jwtKey     []byte
	issuer     string
	cfg        Config
	now        func() time.Time
	dummyHash  string
}

func NewService(repo Repository, challenges ChallengeStore, refreshes RefreshStore, replays ReplayStore, key, jwtSecret []byte, cfg Config) (*Service, error) {
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
	// Prefer a dedicated JWT secret; fall back to a key derived from the
	// admin secret key. No key at all (CLI bearer mode) means no access
	// tokens can be issued or verified.
	jwtKey := jwtSecret
	if len(jwtKey) == 0 && len(key) > 0 {
		jwtKey = accessKey(key)
	}
	return &Service{repo: repo, challenges: challenges, refreshes: refreshes, replays: replays, cipher: c, jwtKey: jwtKey, issuer: "Pickhour", cfg: cfg, now: time.Now, dummyHash: dummy}, nil
}

func (s *Service) AccessTTL() time.Duration  { return s.cfg.AccessTTL }
func (s *Service) RefreshTTL() time.Duration { return s.cfg.RefreshTTL }

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

	// Reuse a still-valid challenge for this user instead of minting a new
	// one: repeated logins (wrong TOTP, page refresh) must not pile up
	// challenges or invalidate an in-progress enrollment.
	if pc, err := s.challenges.ForUser(ctx, user.ID); err == nil {
		needsEnroll := user.TotpConfirmedAt == nil || len(user.TotpSecret) == 0
		if pc.ExpiresAt.After(s.now()) &&
			((pc.Kind == ChallengeEnroll && needsEnroll) ||
				(pc.Kind == ChallengeLogin && !needsEnroll)) {
			if pc.Kind == ChallengeEnroll {
				return s.enrollResult(pc, email)
			}
			return LoginResult{Challenge: pc.Token, Mode: "totp"}, nil
		}
		// Stale kind (e.g. enrollment finished since) — drop it.
		_ = s.challenges.Delete(ctx, pc)
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
		if err := s.challenges.Put(ctx, &PendingChallenge{
			Challenge: Challenge{
				TokenHash: hash, AdminUserID: user.ID, Kind: ChallengeEnroll,
				TotpSecret: sealed, ExpiresAt: expires,
			},
			Token: token,
		}); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Challenge: token, Mode: "enroll", EnrollSecret: secret, OTPAuthURL: url}, nil
	}

	if err := s.challenges.Put(ctx, &PendingChallenge{
		Challenge: Challenge{
			TokenHash: hash, AdminUserID: user.ID, Kind: ChallengeLogin, ExpiresAt: expires,
		},
		Token: token,
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Challenge: token, Mode: "totp"}, nil
}

// enrollResult rebuilds the enroll response for a reused challenge: the
// sealed TOTP secret is opened and the otpauth URL regenerated from it.
func (s *Service) enrollResult(pc *PendingChallenge, email string) (LoginResult, error) {
	secret, err := s.openSecret(pc.TotpSecret)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Challenge:    pc.Token,
		Mode:         "enroll",
		EnrollSecret: secret,
		OTPAuthURL:   TOTPURL(s.issuer, email, secret),
	}, nil
}

func (s *Service) challengeUser(ctx context.Context, token string, wantKind string) (*PendingChallenge, *User, error) {
	pc, err := s.challenges.ByTokenHash(ctx, HashToken(token))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidChallenge
		}
		return nil, nil, err
	}
	ch := &pc.Challenge
	if ch.Kind != wantKind || !ch.ExpiresAt.After(s.now()) {
		_ = s.challenges.Delete(ctx, pc)
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
	return pc, user, nil
}

func (s *Service) verifyTOTPStep(ctx context.Context, userID uuid.UUID, secretB32, code string) bool {
	counter, ok := ValidateTOTP(secretB32, code, s.now())
	if !ok {
		return false
	}
	fresh, err := s.replays.TryUsedTOTP(ctx, userID, counter)
	return err == nil && fresh
}

func (s *Service) ConfirmCode(ctx context.Context, challenge, code, userAgent, ip string) (*AuthResult, *TokenPair, error) {
	pc, user, err := s.challengeUser(ctx, challenge, ChallengeLogin)
	if err != nil {
		return nil, nil, err
	}

	code = strings.TrimSpace(code)
	var ok bool
	if len(code) == totpDigits {
		secret, err := s.openSecret(user.TotpSecret)
		if err != nil {
			return nil, nil, err
		}
		ok = s.verifyTOTPStep(ctx, user.ID, secret, code)
	} else {
		ok, err = s.repo.ConsumeRecoveryCode(ctx, user.ID, HashRecoveryCode(code))
		if err != nil {
			return nil, nil, err
		}
	}
	if !ok {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return nil, nil, ErrInvalidCode
	}

	// Consume the challenge: it is single-use, same as the enroll path.
	_ = s.challenges.Delete(ctx, pc)

	pair, err := s.openSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, nil, err
	}
	return &AuthResult{User: info(user)}, pair, nil
}

func (s *Service) ConfirmEnroll(ctx context.Context, challenge, code, userAgent, ip string) (*AuthResult, *TokenPair, error) {
	pc, user, err := s.challengeUser(ctx, challenge, ChallengeEnroll)
	if err != nil {
		return nil, nil, err
	}
	// Enrollment is single-shot: a stale enroll challenge must not overwrite
	// an already-confirmed TOTP secret or wipe the recovery codes.
	if user.TotpConfirmedAt != nil {
		return nil, nil, ErrInvalidChallenge
	}
	cipher, err := s.requireCipher()
	if err != nil {
		return nil, nil, err
	}
	secret, err := cipher.Open(pc.TotpSecret)
	if err != nil {
		return nil, nil, err
	}
	if !s.verifyTOTPStep(ctx, user.ID, string(secret), strings.TrimSpace(code)) {
		_ = s.repo.RecordAuthFailure(ctx, user.ID, s.cfg.MaxFailures, s.cfg.Lockout)
		return nil, nil, ErrInvalidCode
	}

	codes, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, nil, err
	}
	if err := s.repo.ConfirmTOTP(ctx, user.ID, pc.TotpSecret); err != nil {
		return nil, nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, user.ID, hashes); err != nil {
		return nil, nil, err
	}
	_ = s.challenges.Delete(ctx, pc)
	_ = s.repo.ResetAuthFailures(ctx, user.ID)

	pair, err := s.openSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, nil, err
	}
	return &AuthResult{User: info(user), RecoveryCodes: codes}, pair, nil
}

// issueSession mints a new access JWT + single-use refresh token and stores
// the refresh entry. sid stays stable across rotations of the same login.
func (s *Service) issueSession(ctx context.Context, user *User, sid uuid.UUID, userAgent, ip string, createdAt time.Time) (*TokenPair, error) {
	access, err := s.signAccess(user, sid)
	if err != nil {
		return nil, err
	}
	refresh, hash, err := NewSessionToken()
	if err != nil {
		return nil, err
	}
	expiresAt := createdAt.Add(s.cfg.RefreshTTL).UTC()
	entry := &RefreshEntry{
		ID:        sid,
		UserID:    user.ID,
		Email:     user.Email,
		UserAgent: userAgent,
		IP:        ip,
		Hash:      hex.EncodeToString(hash),
		CreatedAt: createdAt.UTC(),
		ExpiresAt: expiresAt,
	}
	// Refresh TTL is absolute from the original login (createdAt is carried
	// across rotations), so the store TTL is the remaining lifetime.
	if err := s.refreshes.Put(ctx, hash, entry, expiresAt.Sub(s.now())); err != nil {
		return nil, err
	}
	return &TokenPair{Access: access, Refresh: refresh}, nil
}

func (s *Service) openSession(ctx context.Context, user *User, userAgent, ip string) (*TokenPair, error) {
	pair, err := s.issueSession(ctx, user, uuid.New(), userAgent, ip, s.now())
	if err != nil {
		return nil, err
	}
	_ = s.repo.TouchLastLogin(ctx, user.ID)
	_ = s.repo.ResetAuthFailures(ctx, user.ID)
	return pair, nil
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

// Authenticate verifies a stateless access JWT and loads the user so a
// disabled account is cut off within the access TTL. Session revocation
// happens on the refresh path.
func (s *Service) Authenticate(ctx context.Context, token string) (*Session, *User, error) {
	if token == "" {
		return nil, nil, ErrInvalidSession
	}
	claims, err := s.parseAccess(token)
	if err != nil {
		return nil, nil, err
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, nil, ErrInvalidSession
	}
	sid, _ := uuid.Parse(claims.SID)
	user, err := s.repo.UserByID(ctx, uid)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidSession
		}
		return nil, nil, err
	}
	if user.DisabledAt != nil || claims.Epoch != user.AuthEpoch {
		return nil, nil, ErrInvalidSession
	}
	return &Session{ID: sid, UserID: user.ID}, user, nil
}

// RefreshSession rotates a refresh token: the old token is consumed
// atomically (single-use), then a fresh access+refresh pair is issued under
// the same session id. Replays and concurrent refreshes fail.
func (s *Service) RefreshSession(ctx context.Context, refreshToken, userAgent, ip string) (*TokenPair, *User, error) {
	if refreshToken == "" {
		return nil, nil, ErrInvalidSession
	}
	entry, err := s.refreshes.Consume(ctx, HashToken(refreshToken))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidSession
		}
		return nil, nil, err
	}
	user, err := s.repo.UserByID(ctx, entry.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.DisabledAt != nil {
		// Disabled account: kill the whole session family, not just this token.
		_ = s.refreshes.DeleteOthers(ctx, user.ID, "")
		return nil, nil, ErrInvalidSession
	}
	pair, err := s.issueSession(ctx, user, entry.ID, userAgent, ip, entry.CreatedAt)
	if err != nil {
		return nil, nil, err
	}
	return pair, user, nil
}

// Logout revokes the refresh session. The access JWT stays valid until its
// short expiry — it is stateless by design.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.refreshes.Delete(ctx, HashToken(refreshToken))
}

func (s *Service) Me(ctx context.Context, token string) (*Session, *User, error) {
	return s.Authenticate(ctx, token)
}

// Sessions lists refresh-backed login sessions for the user that owns the
// presented refresh token.
func (s *Service) Sessions(ctx context.Context, refreshToken string) ([]SessionInfo, error) {
	_, user, err := s.sessionEntry(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	rows, err := s.refreshes.ListByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	currentHash := HashToken(refreshToken)
	out := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionInfo{
			ID:         r.ID,
			CreatedAt:  r.CreatedAt,
			ExpiresAt:  r.ExpiresAt,
			LastSeenAt: r.CreatedAt, // no per-request touch; refresh rotates instead
			UserAgent:  r.UserAgent,
			IP:         r.IP,
			Current:    r.Hash == hex.EncodeToString(currentHash),
		})
	}
	return out, nil
}

// sessionEntry validates a refresh token without consuming it and returns
// its entry plus the owning user.
func (s *Service) sessionEntry(ctx context.Context, refreshToken string) (*RefreshEntry, *User, error) {
	if refreshToken == "" {
		return nil, nil, ErrInvalidSession
	}
	entry, err := s.refreshes.Get(ctx, HashToken(refreshToken))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrInvalidSession
		}
		return nil, nil, err
	}
	user, err := s.repo.UserByID(ctx, entry.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.DisabledAt != nil {
		return nil, nil, ErrInvalidSession
	}
	return entry, user, nil
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

func (s *Service) RevokeOtherSessions(ctx context.Context, refreshToken string) error {
	_, user, err := s.sessionEntry(ctx, refreshToken)
	if err != nil {
		return err
	}
	return s.refreshes.DeleteOthers(ctx, user.ID, hex.EncodeToString(HashToken(refreshToken)))
}

func (s *Service) revokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	return s.refreshes.DeleteOthers(ctx, userID, "")
}

func (s *Service) ChangePassword(ctx context.Context, accessToken, refreshToken, current, next string) error {
	_, sessUser, err := s.Authenticate(ctx, accessToken)
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
	var keep string
	if refreshToken != "" {
		keep = hex.EncodeToString(HashToken(refreshToken))
	}
	if err := s.refreshes.DeleteOthers(ctx, user.ID, keep); err != nil {
		return err
	}
	return s.challenges.DeleteForUser(ctx, user.ID)
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
	if err := s.revokeAllSessions(ctx, user.ID); err != nil {
		return err
	}
	return s.challenges.DeleteForUser(ctx, user.ID)
}

func (s *Service) Reset2FA(ctx context.Context, email string) error {
	user, err := s.userByEmail(ctx, email)
	if err != nil {
		return err
	}
	if err := s.repo.ResetTOTP(ctx, user.ID); err != nil {
		return err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, user.ID, nil); err != nil {
		return err
	}
	if err := s.replays.DeleteForUser(ctx, user.ID); err != nil {
		return err
	}
	return s.challenges.DeleteForUser(ctx, user.ID)
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

		return s.revokeAllSessions(ctx, user.ID)
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

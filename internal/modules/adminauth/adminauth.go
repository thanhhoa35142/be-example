package adminauth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidCode        = errors.New("invalid or expired code")
	ErrInvalidChallenge   = errors.New("invalid or expired sign-in challenge")
	ErrInvalidSession     = errors.New("invalid or expired session")
	ErrAccountLocked      = errors.New("account temporarily locked")
	ErrUserNotFound       = errors.New("admin user not found")
	ErrUserExists         = errors.New("admin user already exists")
	ErrWrongPassword      = errors.New("current password is incorrect")
	ErrWeakPassword       = errors.New("password is too short")
	ErrPasswordUnchanged  = errors.New("new password must differ from the current one")
)

type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	PasswordHash    string     `json:"-"`
	TotpSecret      []byte     `json:"-"`
	TotpConfirmedAt *time.Time `json:"totpConfirmedAt"`
	DisabledAt      *time.Time `json:"disabledAt"`
	LastLoginAt     *time.Time `json:"lastLoginAt"`
	FailedAttempts  int        `json:"-"`
	LockedUntil     *time.Time `json:"-"`
	AuthEpoch       int64      `json:"-"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Session is the authenticated-admin identity resolved from an access token.
// ID is the session id (sid claim), stable across refresh rotations.
type Session struct {
	ID     uuid.UUID `json:"id"`
	UserID uuid.UUID `json:"-"`
}

// TokenPair is issued at login/enroll and on every refresh rotation.
// Access is short-lived (minutes); refresh is long-lived and single-use.
type TokenPair struct {
	Access  string `json:"-"`
	Refresh string `json:"-"`
}

type SessionInfo struct {
	ID         uuid.UUID `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	UserAgent  string    `json:"userAgent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

const MinPasswordLength = 12

const (
	ChallengeLogin  = "login"
	ChallengeEnroll = "enroll"
)

type Challenge struct {
	TokenHash   []byte
	AdminUserID uuid.UUID
	Kind        string
	TotpSecret  []byte
	ExpiresAt   time.Time
}

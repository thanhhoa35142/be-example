package adminauth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	UserByEmail(ctx context.Context, email string) (*User, error)
	UserByID(ctx context.Context, id uuid.UUID) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, email, passwordHash string) (*User, error)
	SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	TouchLastLogin(ctx context.Context, id uuid.UUID) error
	RecordAuthFailure(ctx context.Context, id uuid.UUID, maxFailures int, lockout time.Duration) error
	ResetAuthFailures(ctx context.Context, id uuid.UUID) error
	ConfirmTOTP(ctx context.Context, id uuid.UUID, sealedSecret []byte) error
	ResetTOTP(ctx context.Context, id uuid.UUID) error
	SetDisabled(ctx context.Context, id uuid.UUID, disabled bool) error

	CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, userAgent, ip string, expiresAt time.Time) (*Session, error)
	SessionByToken(ctx context.Context, tokenHash []byte) (*Session, *User, error)
	TouchSession(ctx context.Context, sessionID uuid.UUID) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error)
	RevokeOtherSessions(ctx context.Context, userID uuid.UUID, keepTokenHash []byte) error

	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes [][]byte) error
	ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash []byte) (bool, error)
	TryUsedTOTP(ctx context.Context, userID uuid.UUID, step int64) (bool, error)
	DeleteTOTPUsed(ctx context.Context, userID uuid.UUID) error
	PruneTOTPUsed(ctx context.Context) error
}

package persistence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

type AdminAuthRepo struct {
	q *Queries
}

func NewAdminAuthRepo(q *Queries) *AdminAuthRepo {
	return &AdminAuthRepo{q: q}
}

func mapAdminUser(row TAdminUser) *adminauth.User {
	return &adminauth.User{
		ID:              row.ID,
		Email:           row.Email,
		PasswordHash:    row.PasswordHash,
		TotpSecret:      row.TotpSecret,
		TotpConfirmedAt: tsPtr(row.TotpConfirmedAt),
		DisabledAt:      tsPtr(row.DisabledAt),
		LastLoginAt:     tsPtr(row.LastLoginAt),
		FailedAttempts:  int(row.FailedAttempts),
		LockedUntil:     tsPtr(row.LockedUntil),
		CreatedAt:       row.CreatedAt.UTC(),
	}
}

func mapAdminSession(row TAdminSession) *adminauth.Session {
	return &adminauth.Session{
		ID:         row.ID,
		TokenHash:  row.TokenHash,
		UserID:     row.AdminUserID,
		UserAgent:  row.UserAgent,
		IP:         row.Ip,
		CreatedAt:  row.CreatedAt.UTC(),
		ExpiresAt:  row.ExpiresAt.UTC(),
		LastSeenAt: row.LastSeenAt.UTC(),
	}
}

func (r *AdminAuthRepo) UserByEmail(ctx context.Context, email string) (*adminauth.User, error) {
	row, err := r.q.GetAdminUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return mapAdminUser(row), nil
}

func (r *AdminAuthRepo) UserByID(ctx context.Context, id uuid.UUID) (*adminauth.User, error) {
	row, err := r.q.GetAdminUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return mapAdminUser(row), nil
}

func (r *AdminAuthRepo) ListUsers(ctx context.Context) ([]adminauth.User, error) {
	rows, err := r.q.ListAdminUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]adminauth.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminauth.User{
			Email:           row.Email,
			TotpConfirmedAt: tsPtr(row.TotpConfirmedAt),
			DisabledAt:      tsPtr(row.DisabledAt),
			LastLoginAt:     tsPtr(row.LastLoginAt),
		})
	}
	return out, nil
}

func (r *AdminAuthRepo) CreateUser(ctx context.Context, email, passwordHash string) (*adminauth.User, error) {
	row, err := r.q.InsertAdminUser(ctx, InsertAdminUserParams{Email: email, PasswordHash: passwordHash})
	if err != nil {
		return nil, err
	}
	return mapAdminUser(row), nil
}

func (r *AdminAuthRepo) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	return r.q.UpdateAdminUserPassword(ctx, UpdateAdminUserPasswordParams{ID: id, PasswordHash: passwordHash})
}

func (r *AdminAuthRepo) TouchLastLogin(ctx context.Context, id uuid.UUID) error {
	return r.q.TouchAdminUserLogin(ctx, id)
}

func (r *AdminAuthRepo) RecordAuthFailure(ctx context.Context, id uuid.UUID, maxFailures int, lockout time.Duration) error {
	return r.q.RecordAdminAuthFailure(ctx, RecordAdminAuthFailureParams{
		ID:             id,
		MaxFailures:    int32(maxFailures),
		LockoutSeconds: lockout.Seconds(),
	})
}

func (r *AdminAuthRepo) ResetAuthFailures(ctx context.Context, id uuid.UUID) error {
	return r.q.ResetAdminAuthFailures(ctx, id)
}

func (r *AdminAuthRepo) ConfirmTOTP(ctx context.Context, id uuid.UUID, sealedSecret []byte) error {
	return r.q.ConfirmAdminUserTOTP(ctx, ConfirmAdminUserTOTPParams{ID: id, TotpSecret: sealedSecret})
}

func (r *AdminAuthRepo) ResetTOTP(ctx context.Context, id uuid.UUID) error {
	return r.q.ResetAdminUserTOTP(ctx, id)
}

func (r *AdminAuthRepo) SetDisabled(ctx context.Context, id uuid.UUID, disabled bool) error {
	return r.q.SetAdminUserDisabled(ctx, SetAdminUserDisabledParams{ID: id, Disabled: disabled})
}

func (r *AdminAuthRepo) CreateChallenge(ctx context.Context, ch adminauth.Challenge) error {
	return r.q.CreateAdminAuthChallenge(ctx, CreateAdminAuthChallengeParams{
		TokenHash:   ch.TokenHash,
		AdminUserID: ch.AdminUserID,
		Kind:        ch.Kind,
		TotpSecret:  ch.TotpSecret,
		ExpiresAt:   ch.ExpiresAt.UTC(),
	})
}

func (r *AdminAuthRepo) ChallengeByToken(ctx context.Context, tokenHash []byte) (*adminauth.Challenge, error) {
	row, err := r.q.GetAdminAuthChallenge(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	return &adminauth.Challenge{
		TokenHash:   row.TokenHash,
		AdminUserID: row.AdminUserID,
		Kind:        row.Kind,
		TotpSecret:  row.TotpSecret,
		ExpiresAt:   row.ExpiresAt.UTC(),
	}, nil
}

func (r *AdminAuthRepo) DeleteChallenge(ctx context.Context, tokenHash []byte) error {
	return r.q.DeleteAdminAuthChallenge(ctx, tokenHash)
}

func (r *AdminAuthRepo) DeleteChallengesForUser(ctx context.Context, userID uuid.UUID) error {
	return r.q.DeleteAdminAuthChallengesForUser(ctx, userID)
}

func (r *AdminAuthRepo) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, userAgent, ip string, expiresAt time.Time) (*adminauth.Session, error) {
	row, err := r.q.CreateAdminSession(ctx, CreateAdminSessionParams{
		TokenHash:   tokenHash,
		AdminUserID: userID,
		UserAgent:   userAgent,
		Ip:          ip,
		ExpiresAt:   expiresAt.UTC(),
	})
	if err != nil {
		return nil, err
	}
	return mapAdminSession(row), nil
}

func (r *AdminAuthRepo) SessionByToken(ctx context.Context, tokenHash []byte) (*adminauth.Session, *adminauth.User, error) {
	row, err := r.q.GetAdminSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, nil, err
	}
	return &adminauth.Session{
			ID:         row.ID,
			TokenHash:  row.TokenHash,
			UserID:     row.AdminUserID,
			UserAgent:  row.UserAgent,
			IP:         row.Ip,
			CreatedAt:  row.CreatedAt.UTC(),
			ExpiresAt:  row.ExpiresAt.UTC(),
			LastSeenAt: row.LastSeenAt.UTC(),
		}, &adminauth.User{
			ID:              row.AdminUserID,
			Email:           row.UserEmail,
			DisabledAt:      tsPtr(row.UserDisabledAt),
			TotpConfirmedAt: tsPtr(row.UserTotpConfirmedAt),
		}, nil
}

func (r *AdminAuthRepo) TouchSession(ctx context.Context, sessionID uuid.UUID) error {
	return r.q.TouchAdminSession(ctx, sessionID)
}

func (r *AdminAuthRepo) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return r.q.DeleteAdminSession(ctx, tokenHash)
}

func (r *AdminAuthRepo) ListSessions(ctx context.Context, userID uuid.UUID) ([]adminauth.Session, error) {
	rows, err := r.q.ListAdminSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]adminauth.Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, *mapAdminSession(row))
	}
	return out, nil
}

func (r *AdminAuthRepo) RevokeOtherSessions(ctx context.Context, userID uuid.UUID, keepTokenHash []byte) error {
	var keep []byte
	if keepTokenHash != nil {
		keep = keepTokenHash
	}
	return r.q.RevokeOtherAdminSessions(ctx, RevokeOtherAdminSessionsParams{
		AdminUserID:   userID,
		KeepTokenHash: keep,
	})
}

func (r *AdminAuthRepo) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes [][]byte) error {
	if len(hashes) == 0 {
		return r.q.DeleteAdminRecoveryCodes(ctx, userID)
	}
	if err := r.q.DeleteAdminRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	return r.q.InsertAdminRecoveryCodes(ctx, InsertAdminRecoveryCodesParams{
		AdminUserID: userID,
		CodeHashes:  hashes,
	})
}

func (r *AdminAuthRepo) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash []byte) (bool, error) {
	n, err := r.q.ConsumeAdminRecoveryCode(ctx, ConsumeAdminRecoveryCodeParams{
		AdminUserID: userID,
		CodeHash:    codeHash,
	})
	return n > 0, err
}

func (r *AdminAuthRepo) TryUsedTOTP(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	n, err := r.q.TryInsertAdminTOTPStep(ctx, TryInsertAdminTOTPStepParams{
		AdminUserID: userID,
		Step:        step,
	})
	return n > 0, err
}

func (r *AdminAuthRepo) DeleteTOTPUsed(ctx context.Context, userID uuid.UUID) error {
	return r.q.DeleteAdminTOTPUsedForUser(ctx, userID)
}

func (r *AdminAuthRepo) PruneTOTPUsed(ctx context.Context) error {
	return r.q.PruneAdminTOTPUsed(ctx)
}

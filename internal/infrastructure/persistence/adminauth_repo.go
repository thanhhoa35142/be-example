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
		AuthEpoch:       row.AuthEpoch,
		CreatedAt:       row.CreatedAt.UTC(),
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

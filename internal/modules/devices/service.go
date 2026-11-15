package devices

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RegisterResult struct {
	ID     uuid.UUID
	Secret *string // set only when a credential was issued by this call
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	in, err := in.Normalize()
	if err != nil {
		return RegisterResult{}, err
	}

	stored, err := s.repo.SecretHash(ctx, in.ID)
	switch {
	case err == nil && stored != "":
		// Device already holds a credential: re-registration must prove it.
		// Anyone knowing only the UUID must not be able to rotate the APNs
		// token or rewrite prefs.
		if in.TokenHash == "" || stored != in.TokenHash {
			return RegisterResult{}, ErrUnauthorized
		}
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return RegisterResult{}, err
	default:
		// No live credential: first registration or a legacy row. Issue one.
		raw, hash, err := NewSecret()
		if err != nil {
			return RegisterResult{}, err
		}
		in.SecretHash = hash
		res, err := s.repo.Upsert(ctx, in)
		if err != nil {
			return RegisterResult{}, err
		}
		if res.SecretHash != hash {
			// A concurrent registration won the race and installed its own
			// credential — ours was discarded by COALESCE. Fail loudly; the
			// client retries and (having no valid token) gets 401, same as
			// any credentialed device.
			return RegisterResult{}, ErrConflict
		}
		return RegisterResult{ID: res.ID, Secret: &raw}, nil
	}

	res, err := s.repo.Upsert(ctx, in)
	if err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{ID: res.ID}, nil
}

// Authenticate verifies a presented device secret against the stored hash.
// Missing device, missing credential or mismatch all return false — callers
// must not distinguish them (no account enumeration).
func (s *Service) Authenticate(ctx context.Context, id uuid.UUID, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	stored, err := s.repo.SecretHash(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored == "" {
		return false, nil
	}
	return SecretMatches(secret, stored), nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.repo.Delete(ctx, id)
}

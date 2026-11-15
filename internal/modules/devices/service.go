package devices

import (
	"context"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (uuid.UUID, error) {
	in, err := in.Normalize()
	if err != nil {
		return uuid.Nil, err
	}
	return s.repo.Upsert(ctx, in)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.repo.Delete(ctx, id)
}

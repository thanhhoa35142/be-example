package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	KeyPrefixTag = "phk_"
	keyRandBytes = 32

	DisplayPrefixLen = 16
)

type IssuedKey struct {
	ApiKey
	Secret string `json:"secret"`
}

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

func HashKey(raw string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return sum[:]
}

func generateSecret() (string, error) {
	buf := make([]byte, keyRandBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return KeyPrefixTag + base64.RawURLEncoding.EncodeToString(buf), nil
}

func validateGrant(g Grant) error {
	if g.Effect != "allow" && g.Effect != "deny" {
		return ErrInvalidGrant
	}
	if g.Resource == "" || g.Action == "" {
		return ErrInvalidGrant
	}
	return nil
}

func (s *Service) Create(ctx context.Context, name, description string, specs []GrantSpec, expiresAt *time.Time, rateLimitRPS int) (IssuedKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return IssuedKey{}, ErrInvalidKey
	}
	if rateLimitRPS <= 0 {
		rateLimitRPS = 60
	}
	grants := make([]Grant, 0, len(specs))
	for _, sp := range specs {
		g := Grant{Effect: sp.Effect, Resource: sp.Resource, Action: sp.Action}
		if err := validateGrant(g); err != nil {
			return IssuedKey{}, err
		}
		grants = append(grants, g)
	}

	secret, err := generateSecret()
	if err != nil {
		return IssuedKey{}, err
	}
	prefix := secret
	if len(prefix) > DisplayPrefixLen {
		prefix = prefix[:DisplayPrefixLen]
	}
	key := ApiKey{
		Name:         name,
		Description:  description,
		KeyPrefix:    prefix,
		RateLimitRPS: rateLimitRPS,
		ExpiresAt:    expiresAt,
		CreatedAt:    s.now().UTC(),
	}
	if err := s.repo.CreateKey(ctx, key, HashKey(secret), grants); err != nil {
		return IssuedKey{}, err
	}

	stored, err := s.repo.FindByHash(ctx, HashKey(secret))
	if err != nil {
		return IssuedKey{}, err
	}
	return IssuedKey{ApiKey: stored, Secret: secret}, nil
}

func (s *Service) Authenticate(ctx context.Context, raw string) (*Auth, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, KeyPrefixTag) {
		return nil, ErrInvalidKey
	}
	key, err := s.repo.FindByHash(ctx, HashKey(raw))
	if err != nil {
		return nil, ErrKeyNotFound
	}
	now := s.now()
	if key.RevokedAt != nil {
		return nil, ErrKeyRevoked
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return nil, ErrKeyExpired
	}
	grants, err := s.repo.GrantsForKey(ctx, key.ID)
	if err != nil {
		return nil, err
	}

	go func() {
		bg := context.WithoutCancel(ctx)
		_ = s.repo.TouchLastUsed(bg, key.ID)
	}()
	return &Auth{Key: key, Grants: grants}, nil
}

func (s *Service) Authorize(auth *Auth, resource, action string) error {
	return auth.Authorize(resource, action)
}

func (s *Service) List(ctx context.Context) ([]ApiKey, error) {
	return s.repo.ListKeys(ctx)
}

func (s *Service) ListWithGrants(ctx context.Context) ([]KeyWithGrants, error) {
	keys, err := s.repo.ListKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]KeyWithGrants, 0, len(keys))
	for _, k := range keys {
		grants, err := s.repo.GrantsForKey(ctx, k.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, KeyWithGrants{ApiKey: k, Grants: grants})
	}
	return out, nil
}

func (s *Service) Revoke(ctx context.Context, id uuid.UUID) error {
	ok, err := s.repo.Revoke(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrKeyNotFound
	}
	return nil
}

func (s *Service) Grants(ctx context.Context, id uuid.UUID) ([]Grant, error) {
	return s.repo.GrantsForKey(ctx, id)
}

func (s *Service) AddGrant(ctx context.Context, id uuid.UUID, g Grant) error {
	if err := validateGrant(g); err != nil {
		return err
	}
	return s.repo.AddGrant(ctx, id, g)
}

func (s *Service) RemoveGrant(ctx context.Context, id uuid.UUID, g Grant) error {
	ok, err := s.repo.RemoveGrant(ctx, id, g)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidGrant
	}
	return nil
}

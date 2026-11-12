package apikeys

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuthorizeDenyWins(t *testing.T) {
	auth := &Auth{Grants: []Grant{
		{Effect: "allow", Resource: "*", Action: "*"},
		{Effect: "deny", Resource: "admin", Action: "read"},
	}}
	if err := auth.Authorize("spots", "read"); err != nil {
		t.Fatalf("wildcard allow should permit spots:read, got %v", err)
	}
	if err := auth.Authorize("admin", "read"); err != ErrForbidden {
		t.Fatalf("deny should win over wildcard allow, got %v", err)
	}
	if err := auth.Authorize("admin", "write"); err != nil {
		t.Fatalf("deny scoped to action=read should not block write, got %v", err)
	}
}

func TestAuthorizeRequiresAllow(t *testing.T) {
	auth := &Auth{Grants: []Grant{
		{Effect: "allow", Resource: "spots", Action: "read"},
	}}
	if err := auth.Authorize("spots", "read"); err != nil {
		t.Fatalf("exact match should allow, got %v", err)
	}
	if err := auth.Authorize("spots", "write"); err != ErrForbidden {
		t.Fatalf("unmatched action should be forbidden, got %v", err)
	}
	if err := auth.Authorize("reports", "read"); err != ErrForbidden {
		t.Fatalf("unmatched resource should be forbidden, got %v", err)
	}
}

func TestAuthorizeWildcardFields(t *testing.T) {
	auth := &Auth{Grants: []Grant{
		{Effect: "allow", Resource: "*", Action: "read"},
	}}
	if err := auth.Authorize("anything", "read"); err != nil {
		t.Fatalf("resource wildcard should allow, got %v", err)
	}
	if err := auth.Authorize("anything", "write"); err != ErrForbidden {
		t.Fatalf("action not covered should be forbidden, got %v", err)
	}
}

// fakeRepo is an in-memory Repository for service tests.
type fakeRepo struct {
	key    ApiKey
	grants []Grant
	hash   []byte
}

func (f *fakeRepo) CreateKey(_ context.Context, k ApiKey, keyHash []byte, grants []Grant) error {
	f.key = k
	f.hash = keyHash
	f.grants = grants
	return nil
}
func (f *fakeRepo) FindByHash(_ context.Context, keyHash []byte) (ApiKey, error) {
	if string(f.hash) != string(keyHash) {
		return ApiKey{}, ErrKeyNotFound
	}
	k := f.key
	k.ID = uuid.New()
	return k, nil
}
func (f *fakeRepo) GrantsForKey(_ context.Context, _ uuid.UUID) ([]Grant, error) {
	return f.grants, nil
}
func (f *fakeRepo) TouchLastUsed(_ context.Context, _ uuid.UUID) error  { return nil }
func (f *fakeRepo) ListKeys(_ context.Context) ([]ApiKey, error)        { return nil, nil }
func (f *fakeRepo) Revoke(_ context.Context, _ uuid.UUID) (bool, error) { return false, nil }
func (f *fakeRepo) AddGrant(_ context.Context, _ uuid.UUID, _ Grant) error {
	return nil
}
func (f *fakeRepo) RemoveGrant(_ context.Context, _ uuid.UUID, _ Grant) (bool, error) {
	return false, nil
}

func TestAuthenticateRejectsBadPrefix(t *testing.T) {
	svc := NewService(&fakeRepo{})
	if _, err := svc.Authenticate(context.Background(), "not-a-key"); err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestAuthenticateRejectsRevokedAndExpired(t *testing.T) {
	for name, mutate := range map[string]func(*ApiKey){
		"revoked": func(k *ApiKey) {
			now := time.Now()
			k.RevokedAt = &now
		},
		"expired": func(k *ApiKey) {
			past := time.Now().Add(-time.Hour)
			k.ExpiresAt = &past
		},
	} {
		repo := &fakeRepo{key: ApiKey{Name: "k"}, grants: []Grant{{Effect: "allow", Resource: "*", Action: "*"}}}
		mutate(&repo.key)
		// Store under the hash of a real-looking key so lookup succeeds.
		raw := KeyPrefixTag + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		repo.hash = HashKey(raw)
		svc := NewService(repo)
		if _, err := svc.Authenticate(context.Background(), raw); err == nil {
			t.Fatalf("%s key should be rejected", name)
		}
	}
}

func TestCreateAndAuthorize(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	issued, err := svc.Create(context.Background(), "agent", "test", []GrantSpec{
		{Effect: "allow", Resource: "spots", Action: "read"},
	}, nil, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(issued.Secret, KeyPrefixTag) {
		t.Fatalf("secret missing prefix: %q", issued.Secret)
	}
	if issued.KeyPrefix != issued.Secret[:DisplayPrefixLen] {
		t.Fatalf("prefix mismatch")
	}

	auth, err := svc.Authenticate(context.Background(), issued.Secret)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := svc.Authorize(auth, "spots", "read"); err != nil {
		t.Fatalf("spots:read should be allowed, got %v", err)
	}
	if err := svc.Authorize(auth, "admin", "read"); err != ErrForbidden {
		t.Fatalf("admin:read should be forbidden, got %v", err)
	}
}

package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type memRepo struct {
	hashes map[uuid.UUID]string
	upsert []RegisterInput
}

func newMemRepo() *memRepo { return &memRepo{hashes: map[uuid.UUID]string{}} }

func (m *memRepo) Upsert(_ context.Context, in RegisterInput) (UpsertResult, error) {
	m.upsert = append(m.upsert, in)
	h := in.SecretHash
	if h == "" {
		h = m.hashes[in.ID] // credentialed path: hash untouched
	}
	m.hashes[in.ID] = h
	return UpsertResult{ID: in.ID, SecretHash: h}, nil
}

func (m *memRepo) SecretHash(_ context.Context, id uuid.UUID) (string, error) {
	if h, ok := m.hashes[id]; ok && h != "" {
		return h, nil
	}
	return "", pgx.ErrNoRows
}

func (m *memRepo) Exists(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (m *memRepo) Delete(context.Context, uuid.UUID) (bool, error) { return true, nil }

func TestRegisterIssuesSecretOnce(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	id := uuid.New()

	out, err := svc.Register(context.Background(), RegisterInput{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if out.Secret == nil || *out.Secret == "" {
		t.Fatal("first registration must return a secret")
	}
	if repo.hashes[id] != HashSecret(*out.Secret) {
		t.Fatal("stored hash must be SHA-256 of the issued secret")
	}

	// Re-registration without the token is rejected — UUID alone is not a
	// credential.
	if _, err := svc.Register(context.Background(), RegisterInput{ID: id}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("re-register without token: %v, want ErrUnauthorized", err)
	}

	// Wrong token also rejected.
	bad := RegisterInput{ID: id, TokenHash: HashSecret("wrong")}
	if _, err := svc.Register(context.Background(), bad); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("re-register with wrong token: %v, want ErrUnauthorized", err)
	}

	// Correct token passes and does not rotate the credential.
	good := RegisterInput{ID: id, TokenHash: HashSecret(*out.Secret)}
	out2, err := svc.Register(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Secret != nil {
		t.Fatal("credentialed re-register must not return a new secret")
	}
	if repo.hashes[id] != HashSecret(*out.Secret) {
		t.Fatal("credential must not rotate on re-register")
	}
}

func TestAuthenticate(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	id := uuid.New()
	out, err := svc.Register(context.Background(), RegisterInput{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	secret := *out.Secret

	ok, err := svc.Authenticate(context.Background(), id, secret)
	if err != nil || !ok {
		t.Fatalf("valid secret: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"", "wrong", secret + "x"} {
		ok, err := svc.Authenticate(context.Background(), id, bad)
		if err != nil || ok {
			t.Fatalf("secret %q: ok=%v err=%v, want false", bad, ok, err)
		}
	}
	// Unknown device: false, no error (no enumeration).
	ok, err = svc.Authenticate(context.Background(), uuid.New(), secret)
	if err != nil || ok {
		t.Fatalf("unknown device: ok=%v err=%v", ok, err)
	}
}

// A live row whose secret_hash is NULL (pre-migration installs) takes the
// same path as a missing row: SecretHash returns "" with no error, which the
// service treats as "no credential" and issues one.
func TestRegisterLegacyRowWithoutHashGetsSecret(t *testing.T) {
	repo := &legacyRepo{newMemRepo()}
	svc := NewService(repo)
	out, err := svc.Register(context.Background(), RegisterInput{ID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Secret == nil {
		t.Fatal("legacy row without credential must be issued a secret")
	}
}

// legacyRepo simulates a row that exists but has secret_hash NULL:
// SecretHash returns ("", nil) instead of pgx.ErrNoRows.
type legacyRepo struct{ *memRepo }

func (legacyRepo) SecretHash(context.Context, uuid.UUID) (string, error) { return "", nil }

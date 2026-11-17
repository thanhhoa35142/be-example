package adminauth

import (
	"crypto/hmac"
	"crypto/sha256"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type accessClaims struct {
	Email string `json:"email"`
	SID   string `json:"sid"`
	Epoch int64  `json:"ep"`
	jwt.RegisteredClaims
}

// accessKey derives a dedicated HMAC key from the admin secret key so the
// AES key used for TOTP secrets is never reused directly for JWT signing.
func accessKey(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("pickhour-admin-access-jwt-v1"))
	return mac.Sum(nil)
}

func (s *Service) signAccess(user *User, sid uuid.UUID) (string, error) {
	if len(s.jwtKey) == 0 {
		return "", ErrKeyMissing
	}
	now := s.now()
	claims := accessClaims{
		Email: user.Email,
		SID:   sid.String(),
		Epoch: user.AuthEpoch,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtKey)
}

func (s *Service) parseAccess(token string) (*accessClaims, error) {
	if len(s.jwtKey) == 0 {
		return nil, ErrInvalidSession
	}
	var claims accessClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidSession
		}
		return s.jwtKey, nil
	})
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidSession
	}
	return &claims, nil
}

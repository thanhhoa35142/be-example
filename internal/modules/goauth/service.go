package goauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
)

const (
	authURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL    = "https://oauth2.googleapis.com/token"
	userinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	revokeURL   = "https://oauth2.googleapis.com/revoke"
	stateTTL    = 10 * time.Minute
	httpTimeout = 15 * time.Second
)

// Scopes requested at consent time: identity (email) + read-only access to
// Search Console and GA4.
const scopeString = "openid email " +
	"https://www.googleapis.com/auth/webmasters.readonly " +
	"https://www.googleapis.com/auth/analytics.readonly"

type Service struct {
	repo   Repository
	cipher *adminauth.Cipher
	key    []byte
	cfg    Config
	http   *http.Client
	now    func() time.Time
}

func NewService(repo Repository, key []byte, cfg Config) (*Service, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("goauth key must be 32 bytes, got %d", len(key))
	}
	cipher, err := adminauth.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Service{
		repo:   repo,
		cipher: cipher,
		key:    key,
		cfg:    cfg,
		http:   &http.Client{Timeout: httpTimeout},
		now:    time.Now,
	}, nil
}

// AuthURL returns the Google consent URL for this admin user. The state
// parameter binds the callback to the user and expires after stateTTL.
func (s *Service) AuthURL(userID uuid.UUID) (string, error) {
	if !s.cfg.Ready() {
		return "", ErrNotConfigured
	}
	state, err := s.signState(userID)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"client_id":     {s.cfg.ClientID},
		"redirect_uri":  {s.cfg.RedirectURL},
		"response_type": {"code"},
		"scope":         {scopeString},
		"access_type":   {"offline"},
		// prompt=consent forces Google to return a refresh_token even when the
		// user has linked before.
		"prompt": {"consent"},
		"state":  {state},
	}
	return authURL + "?" + q.Encode(), nil
}

// HandleCallback completes the flow: verifies state, exchanges the code,
// fetches the Google identity and stores the sealed refresh token.
func (s *Service) HandleCallback(ctx context.Context, userID uuid.UUID, code, state string) error {
	if !s.cfg.Ready() {
		return ErrNotConfigured
	}
	if err := s.verifyState(userID, state); err != nil {
		return err
	}

	tok, err := s.exchangeCode(ctx, code)
	if err != nil {
		return err
	}
	if tok.RefreshToken == "" {
		return ErrNoRefreshToken
	}
	email, sub, err := s.userInfo(ctx, tok.AccessToken)
	if err != nil {
		return err
	}

	sealed, err := s.cipher.Seal([]byte(tok.RefreshToken))
	if err != nil {
		return err
	}
	return s.repo.Upsert(ctx, Token{
		AdminUserID:  userID,
		GoogleSub:    sub,
		GoogleEmail:  email,
		RefreshToken: sealed,
		Scopes:       tok.Scope,
	})
}

func (s *Service) Status(ctx context.Context, userID uuid.UUID) (*Status, error) {
	t, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Status{
		Email:         t.GoogleEmail,
		Scopes:        t.Scopes,
		GSCSiteURL:    t.GSCSiteURL,
		GA4PropertyID: t.GA4PropertyID,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}, nil
}

// SetConfig records which GSC site and GA4 property this link syncs.
// Empty strings clear the selection.
func (s *Service) SetConfig(ctx context.Context, userID uuid.UUID, gscSiteURL, ga4PropertyID string) error {
	if _, err := s.repo.Get(ctx, userID); err != nil {
		return err
	}
	return s.repo.SetConfig(ctx, userID, gscSiteURL, ga4PropertyID)
}

// LinkedAccounts lists every stored link for the sync worker. Tokens stay
// sealed — callers get accounts, not credentials.
func (s *Service) LinkedAccounts(ctx context.Context) ([]Account, error) {
	tokens, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(tokens))
	for _, t := range tokens {
		accounts = append(accounts, Account{
			AdminUserID:   t.AdminUserID,
			GoogleSub:     t.GoogleSub,
			GoogleEmail:   t.GoogleEmail,
			GSCSiteURL:    t.GSCSiteURL,
			GA4PropertyID: t.GA4PropertyID,
			Scopes:        t.Scopes,
		})
	}
	return accounts, nil
}

// AccessToken exchanges the stored refresh token for a short-lived access
// token. Only the sync worker and the resources endpoint call this.
func (s *Service) AccessToken(ctx context.Context, userID uuid.UUID) (string, error) {
	t, err := s.repo.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	plain, err := s.cipher.Open(t.RefreshToken)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"refresh_token": {string(plain)},
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.http.Do(req)
	if err != nil {
		return "", ErrExchangeFailed
	}
	defer func() { _ = res.Body.Close() }()
	var tok tokenResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tok); err != nil ||
		res.StatusCode != http.StatusOK || tok.AccessToken == "" {
		return "", ErrExchangeFailed
	}
	return tok.AccessToken, nil
}

// Disconnect removes the stored link and best-effort revokes the token at
// Google so the grant disappears from the user's account too.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID) error {
	t, err := s.repo.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if plain, err := s.cipher.Open(t.RefreshToken); err == nil {
		s.revoke(ctx, string(plain))
	}
	return s.repo.Delete(ctx, userID)
}

// --- OAuth state (HMAC-signed, stateless) ---

type statePayload struct {
	UID   string `json:"u"`
	Exp   int64  `json:"e"`
	Nonce string `json:"n"`
}

func (s *Service) signState(userID uuid.UUID) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload, err := json.Marshal(statePayload{
		UID:   userID.String(),
		Exp:   s.now().Add(stateTTL).Unix(),
		Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.sign(body)), nil
}

func (s *Service) verifyState(userID uuid.UUID, state string) error {
	body, sig, ok := strings.Cut(state, ".")
	if !ok {
		return ErrStateInvalid
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return ErrStateInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return ErrStateInvalid
	}
	var p statePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ErrStateInvalid
	}
	if p.UID != userID.String() {
		return ErrStateInvalid
	}
	if s.now().Unix() > p.Exp {
		return ErrStateExpired
	}
	return nil
}

func (s *Service) sign(data string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// --- Google endpoints ---

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (s *Service) exchangeCode(ctx context.Context, code string) (*tokenResponse, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
		"redirect_uri":  {s.cfg.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.http.Do(req)
	if err != nil {
		return nil, ErrExchangeFailed
	}
	defer func() { _ = res.Body.Close() }()
	var tok tokenResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tok); err != nil || res.StatusCode != http.StatusOK {
		return nil, ErrExchangeFailed
	}
	return &tok, nil
}

func (s *Service) userInfo(ctx context.Context, accessToken string) (email, sub string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := s.http.Do(req)
	if err != nil {
		return "", "", ErrUserInfoFailed
	}
	defer func() { _ = res.Body.Close() }()
	var info struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&info); err != nil || res.StatusCode != http.StatusOK {
		return "", "", ErrUserInfoFailed
	}
	if info.Email == "" || info.Sub == "" {
		return "", "", ErrUserInfoFailed
	}
	return info.Email, info.Sub, nil
}

func (s *Service) revoke(ctx context.Context, token string) {
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.http.Do(req)
	if err == nil {
		_ = res.Body.Close()
	}
}

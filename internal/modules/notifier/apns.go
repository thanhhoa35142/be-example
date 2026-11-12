package notifier

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// Client sends alert pushes to APNs over HTTP/2 using a provider token
// (ES256 JWT signed with the .p8 key from Apple Developer).
//
// Credentials come from the environment — see ClientFromEnv. When they are
// absent the notifier runs in log-only dev mode and nothing is delivered.
type Client struct {
	key      *ecdsa.PrivateKey
	keyID    string
	teamID   string
	bundleID string
	http     *http.Client

	mu        sync.Mutex
	bearer    string
	bearerExp time.Time
}

// ClientFromEnv builds a Client from APNS_* env vars. Returns (nil, nil)
// when APNs is not configured so callers can run in dev/log mode.
//
//	APNS_KEY_ID     — Key ID of the .p8 (10 chars)
//	APNS_TEAM_ID    — Apple Developer Team ID
//	APNS_KEY        — contents of the .p8 file (PEM; literal \n accepted)
//	APNS_BUNDLE_ID  — optional, defaults to the app bundle id
func ClientFromEnv() (*Client, error) {
	keyID := os.Getenv("APNS_KEY_ID")
	teamID := os.Getenv("APNS_TEAM_ID")
	keyPEM := os.Getenv("APNS_KEY")
	if keyID == "" && teamID == "" && keyPEM == "" {
		return nil, nil
	}
	if keyID == "" || teamID == "" || keyPEM == "" {
		return nil, fmt.Errorf("APNs partially configured: APNS_KEY_ID, APNS_TEAM_ID and APNS_KEY are all required")
	}
	bundleID := os.Getenv("APNS_BUNDLE_ID")
	if bundleID == "" {
		bundleID = "com.example.MyApp"
	}
	return newClient(Config{
		KeyID: keyID, TeamID: teamID, KeyPEM: keyPEM, BundleID: bundleID,
		HTTPClient: httpx.NewClient(httpx.Policy{
			Name: "apns", RatePerSec: 50, Burst: 50, Timeout: 10 * time.Second,
		}),
	})
}

// Config carries APNs credentials explicitly (mainly for tests).
type Config struct {
	KeyID      string
	TeamID     string
	KeyPEM     string
	BundleID   string
	HTTPClient *http.Client // optional; defaults to a plain 10s client
}

func newClient(cfg Config) (*Client, error) {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(cfg.KeyPEM, `\n`, "\n")))
	if block == nil {
		return nil, fmt.Errorf("APNS_KEY is not valid PEM")
	}
	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("APNS_KEY parse: %w", err)
	}
	ecKey, ok := priv.(*ecdsa.PrivateKey)
	if !ok || ecKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("APNS_KEY must be an EC P-256 private key (.p8)")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		key:      ecKey,
		keyID:    cfg.KeyID,
		teamID:   cfg.TeamID,
		bundleID: cfg.BundleID,
		http:     httpClient,
	}, nil
}

// Send posts payload to APNs for one device token.
//
//   - env: "sandbox" or "production" (from device.apns_env; unknown → production)
//   - collapseID: APNs collapse identifier — we use the rule ID so repeated
//     alerts for the same rule coalesce into one banner.
//
// Returns the APNs HTTP status on success (200) or the non-2xx status plus an
// error describing the APNs reason. A 410 means the token is dead — callers
// should clear device.apns_token.
func (c *Client) Send(ctx context.Context, deviceToken, env, collapseID string, payload []byte) (int, error) {
	host := "https://api.push.apple.com"
	if env == "sandbox" {
		host = "https://api.sandbox.push.apple.com"
	}
	url := host + "/3/device/" + deviceToken

	token, err := c.providerToken()
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", c.bundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	if collapseID != "" {
		req.Header.Set("apns-collapse-id", collapseID)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("apns request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("apns %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.StatusCode, nil
}

// providerToken returns a cached ES256 JWT; APNs tokens are valid for 60
// minutes, we refresh after 50.
func (c *Client) providerToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bearer != "" && time.Now().Before(c.bearerExp) {
		return c.bearer, nil
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": c.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": c.teamID, "iat": now.Unix()})
	input := b64url(header) + "." + b64url(claims)

	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("apns jwt sign: %w", err)
	}
	// JWS ES256 wants raw R||S (32 bytes each), not ASN.1 DER.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	c.bearer = input + "." + base64.RawURLEncoding.EncodeToString(sig)
	c.bearerExp = now.Add(50 * time.Minute)
	return c.bearer, nil
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

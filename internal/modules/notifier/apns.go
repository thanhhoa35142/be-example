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

type Config struct {
	KeyID      string
	TeamID     string
	KeyPEM     string
	BundleID   string
	HTTPClient *http.Client
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

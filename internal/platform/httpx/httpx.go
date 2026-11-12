// Package httpx provides an http.RoundTripper that adds per-provider rate
// limiting and retry-with-backoff for outbound calls to third-party APIs.
//
// Design notes:
//   - One limiter per provider (per httpx.Client), shared by all goroutines —
//     correct while workers run a single replica; shard or move to Redis if
//     that ever changes.
//   - Retry-After pauses the whole provider, not just the failing request:
//     when a provider says "slow down", everyone waits.
//   - Only idempotent requests (or requests with a replayable body) are
//     retried on 5xx/network errors; 429 is always safe to retry.
package httpx

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Policy configures rate limiting and retry behavior for one provider.
type Policy struct {
	// Name identifies the provider in logs (e.g. "open-meteo").
	Name string
	// RatePerSec is the sustained request rate; Burst is the bucket size.
	RatePerSec float64
	Burst      int
	// MaxRetries is the number of retries after the first attempt (default 3).
	MaxRetries int
	// BaseDelay/MaxDelay bound the full-jitter backoff when no Retry-After
	// header is present (defaults 500ms / 30s).
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Timeout for the returned http.Client (default 30s).
	Timeout time.Duration
}

func (p Policy) withDefaults() Policy {
	if p.MaxRetries == 0 {
		p.MaxRetries = 3
	}
	if p.BaseDelay == 0 {
		p.BaseDelay = 500 * time.Millisecond
	}
	if p.MaxDelay == 0 {
		p.MaxDelay = 30 * time.Second
	}
	if p.Timeout == 0 {
		p.Timeout = 30 * time.Second
	}
	if p.Burst <= 0 {
		p.Burst = int(p.RatePerSec)
		if p.Burst < 1 {
			p.Burst = 1
		}
	}
	return p
}

// NewClient returns an *http.Client whose transport enforces p.
func NewClient(p Policy) *http.Client {
	p = p.withDefaults()
	return &http.Client{
		Timeout: p.Timeout,
		Transport: &transport{
			base:       http.DefaultTransport,
			policy:     p,
			limiter:    rate.NewLimiter(rate.Limit(p.RatePerSec), p.Burst),
			pauseUntil: &atomic.Int64{},
		},
	}
}

type transport struct {
	base    http.RoundTripper
	policy  Policy
	limiter *rate.Limiter
	// pauseUntil is a unix-nano timestamp; all requests wait it out after a
	// Retry-After response so the whole provider backs off together.
	pauseUntil *atomic.Int64
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := t.waitTurn(req.Context()); err != nil {
			return nil, err
		}
		// Clone so retries get a fresh Body via GetBody.
		r := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r.Body = body
		}
		resp, err := t.base.RoundTrip(r)

		wait, retry := t.classify(req, resp, err, attempt)
		if !retry {
			return resp, err
		}
		drainClose(resp)
		if d := retryAfterDelay(resp); d > 0 {
			t.pauseProvider(d)
			wait = d
		}
		slog.Warn("httpx: retrying",
			"provider", t.policy.Name,
			"method", req.Method,
			"url", req.URL.Host+req.URL.Path,
			"attempt", attempt+1,
			"wait", wait,
			"status", statusOf(resp),
			"err", err,
		)
		if err := sleepCtx(req.Context(), wait); err != nil {
			return nil, err
		}
	}
}

// waitTurn blocks until the provider pause (if any) has elapsed and the
// rate limiter grants a token.
func (t *transport) waitTurn(ctx context.Context) error {
	if until := t.pauseUntil.Load(); until > 0 {
		if d := time.Until(time.Unix(0, until)); d > 0 {
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
		}
	}
	return t.limiter.Wait(ctx)
}

func (t *transport) pauseProvider(d time.Duration) {
	until := time.Now().Add(d).UnixNano()
	for {
		cur := t.pauseUntil.Load()
		if cur >= until || t.pauseUntil.CompareAndSwap(cur, until) {
			return
		}
	}
}

// classify decides whether to retry. Returns the delay to wait.
func (t *transport) classify(req *http.Request, resp *http.Response, err error, attempt int) (time.Duration, bool) {
	if attempt >= t.policy.MaxRetries {
		return 0, false
	}
	if err != nil {
		// Network error — only retry when the request can be replayed.
		if !canReplay(req) {
			return 0, false
		}
		return jitter(t.policy.BaseDelay, t.policy.MaxDelay, attempt), true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		// Rate limited — the request was not processed; always safe to retry.
		return jitter(t.policy.BaseDelay, t.policy.MaxDelay, attempt), true
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		if !canReplay(req) {
			return 0, false
		}
		return jitter(t.policy.BaseDelay, t.policy.MaxDelay, attempt), true
	}
	return 0, false
}

func canReplay(req *http.Request) bool {
	if req.GetBody != nil {
		return true
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// jitter returns a full-jitter delay in [0, min(max, base<<attempt)].
func jitter(base, max time.Duration, attempt int) time.Duration {
	cap := base << attempt
	if cap > max || cap <= 0 {
		cap = max
	}
	return time.Duration(rand.Int64N(int64(cap)))
}

// retryAfterDelay parses the Retry-After header (delta-seconds or HTTP-date).
func retryAfterDelay(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func drainClose(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

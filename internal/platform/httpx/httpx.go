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

type Policy struct {
	Name string

	RatePerSec float64
	Burst      int

	MaxRetries int

	BaseDelay time.Duration
	MaxDelay  time.Duration

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

	pauseUntil *atomic.Int64
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := t.waitTurn(req.Context()); err != nil {
			return nil, err
		}

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

func (t *transport) classify(req *http.Request, resp *http.Response, err error, attempt int) (time.Duration, bool) {
	if attempt >= t.policy.MaxRetries {
		return 0, false
	}
	if err != nil {

		if !canReplay(req) {
			return 0, false
		}
		return jitter(t.policy.BaseDelay, t.policy.MaxDelay, attempt), true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:

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

func jitter(base, max time.Duration, attempt int) time.Duration {
	cap := base << attempt
	if cap > max || cap <= 0 {
		cap = max
	}
	return time.Duration(rand.Int64N(int64(cap)))
}

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

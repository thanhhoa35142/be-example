package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterDelaySeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": {"7"}}}
	if d := retryAfterDelay(resp); d != 7*time.Second {
		t.Fatalf("got %v", d)
	}
}

func TestRetryAfterDelayHTTPDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{
		"Retry-After": {time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)},
	}}
	d := retryAfterDelay(resp)
	if d <= 0 || d > 4*time.Second {
		t.Fatalf("got %v", d)
	}
}

func TestRetryAfterDelayMissing(t *testing.T) {
	if d := retryAfterDelay(&http.Response{}); d != 0 {
		t.Fatalf("got %v", d)
	}
	if d := retryAfterDelay(nil); d != 0 {
		t.Fatalf("got %v", d)
	}
}

func TestJitterBounds(t *testing.T) {
	base := 100 * time.Millisecond
	max := 30 * time.Second
	for i := 0; i < 100; i++ {
		d := jitter(base, max, 2) // cap = 400ms
		if d < 0 || d > 400*time.Millisecond {
			t.Fatalf("attempt 2 out of bounds: %v", d)
		}
	}
	// Overflow attempt → capped at max.
	d := jitter(base, max, 20)
	if d < 0 || d > max {
		t.Fatalf("overflow attempt out of bounds: %v", d)
	}
}

func TestTransportRetries429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	client := NewClient(Policy{
		Name: "test", RatePerSec: 1000, Burst: 100,
		MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond,
	})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestTransportDoesNotRetry404(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(Policy{
		Name: "test", RatePerSec: 1000, Burst: 100,
		MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond,
	})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

func TestTransportRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(Policy{
		Name: "test", RatePerSec: 50, Burst: 1,
		MaxRetries: 0, Timeout: 10 * time.Second,
	})
	start := time.Now()
	for i := 0; i < 3; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	// 3 requests at 50rps with burst 1: first instant, then ~20ms each.
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("requests not rate limited, took %v", elapsed)
	}
}

func TestSleepCtxHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

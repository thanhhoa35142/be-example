package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestAllowAndRetryAfter(t *testing.T) {
	l := New(PerMinute(60, 2)) // 1/s, burst 2
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst request %d denied", i)
		}
	}
	ok, ra := l.Allow("a")
	if ok || ra < time.Second {
		t.Fatalf("expected denial with Retry-After >= 1s, got ok=%v ra=%v", ok, ra)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other key must not be affected")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token should have refilled")
	}
}

func TestDenyOnlyCountsHits(t *testing.T) {
	l := New(PerMinute(1, 3))
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if d, _ := l.Deny("ip"); d {
			t.Fatal("Deny must not consume tokens")
		}
	}
	for i := 0; i < 3; i++ {
		l.Hit("ip")
	}
	if d, ra := l.Deny("ip"); !d || ra < time.Second {
		t.Fatalf("expected denied after 3 hits, got %v %v", d, ra)
	}
}

func TestEvictsIdleKeys(t *testing.T) {
	l := New(PerMinute(60, 5))
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 1000; i++ {
		l.Allow(fmt.Sprintf("ip-%d", i))
	}
	if l.Len() != 1000 {
		t.Fatalf("len = %d", l.Len())
	}
	now = now.Add(10 * time.Minute)
	l.Allow("fresh")
	if l.Len() != 1 {
		t.Fatalf("idle keys not evicted: len = %d", l.Len())
	}
}

func TestDisabled(t *testing.T) {
	l := New(Rule{})
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("x"); !ok {
			t.Fatal("disabled limiter must allow")
		}
	}
}

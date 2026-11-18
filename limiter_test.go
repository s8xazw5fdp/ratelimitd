package ratelimitd

import (
	"testing"
	"time"
)

func newTestLimiter(t *testing.T, rate, burst float64) (*Limiter, *time.Time) {
	t.Helper()
	l, err := New(Params{Rate: rate, Burst: burst})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	l.clock = func() time.Time { return now }
	return l, &now
}

func TestAllowConsumesBurstThenRefills(t *testing.T) {
	l, now := newTestLimiter(t, 2, 3)

	for i := range 3 {
		if d := l.Allow("acme"); !d.Allowed {
			t.Fatalf("request %d rejected while burst lasts", i)
		}
	}
	d := l.Allow("acme")
	if d.Allowed {
		t.Fatal("fourth request allowed, burst is 3")
	}
	if d.RetryAfter != 500*time.Millisecond {
		t.Fatalf("RetryAfter = %v, want 500ms at 2 req/s", d.RetryAfter)
	}

	*now = now.Add(time.Second)
	d = l.Allow("acme")
	if !d.Allowed {
		t.Fatal("request rejected after one second of refill at 2 req/s")
	}
	if d.Remaining != 1 {
		t.Fatalf("Remaining = %v, want 1", d.Remaining)
	}
}

func TestTenantsAreIsolated(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 1)

	if d := l.Allow("acme"); !d.Allowed {
		t.Fatal("first tenant rejected")
	}
	if d := l.Allow("acme"); d.Allowed {
		t.Fatal("second request for same tenant allowed")
	}
	if d := l.Allow("globex"); !d.Allowed {
		t.Fatal("second tenant billed against the first bucket")
	}
}

func TestBucketCapsAtBurst(t *testing.T) {
	l, now := newTestLimiter(t, 100, 2)

	*now = now.Add(time.Hour)
	if d := l.Allow("acme"); d.Remaining != 1 {
		t.Fatalf("Remaining = %v, want 1 after a long idle capped at burst 2", d.Remaining)
	}
}

func TestSweepDropsIdleTenants(t *testing.T) {
	l, now := newTestLimiter(t, 1, 1)

	l.Allow("acme")
	l.Allow("globex")
	if n := l.Sweep(time.Minute); n != 0 {
		t.Fatalf("evicted %d fresh buckets", n)
	}

	*now = now.Add(5 * time.Minute)
	if n := l.Sweep(time.Minute); n != 2 {
		t.Fatalf("evicted %d buckets, want 2", n)
	}
	if got := l.Len(); got != 0 {
		t.Fatalf("Len = %d after sweep, want 0", got)
	}
}

func TestNewRejectsBadParams(t *testing.T) {
	if _, err := New(Params{Rate: 0, Burst: 1}); err != ErrInvalidRate {
		t.Fatalf("err = %v, want ErrInvalidRate", err)
	}
	if _, err := New(Params{Rate: 1, Burst: 0}); err != ErrInvalidBurst {
		t.Fatalf("err = %v, want ErrInvalidBurst", err)
	}
}

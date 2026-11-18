// Package ratelimitd provides per-tenant token-bucket rate limiting for HTTP
// services. Buckets are kept in memory and keyed by tenant; callers decide how
// a tenant is extracted from a request.
package ratelimitd

import (
	"math"
	"sync"
	"time"
)

// DefaultIdleEviction is how long a bucket may go unused before it is dropped.
const DefaultIdleEviction = 10 * time.Minute

// Params describe the shape of every bucket handed out by a Limiter.
type Params struct {
	// Rate is the sustained number of requests per second per tenant.
	Rate float64
	// Burst is the bucket capacity, i.e. the largest tolerated spike.
	Burst float64
}

func (p Params) validate() error {
	switch {
	case p.Rate <= 0:
		return ErrInvalidRate
	case p.Burst < 1:
		return ErrInvalidBurst
	}
	return nil
}

// Decision is the outcome of a single Allow call.
type Decision struct {
	Allowed    bool
	Remaining  float64
	RetryAfter time.Duration
}

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// Limiter hands out per-tenant token buckets. It is safe for concurrent use.
type Limiter struct {
	params Params
	clock  func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New returns a limiter with validated params and a fresh bucket set.
func New(p Params) (*Limiter, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &Limiter{
		params:  p,
		clock:   time.Now,
		buckets: make(map[string]*bucket),
	}, nil
}

// Allow consumes one token from the tenant's bucket. It reports whether the
// request may proceed and how many tokens are left afterwards. An unknown
// tenant is treated as already known at full burst.
func (l *Limiter) Allow(tenant string) Decision {
	b := l.bucketFor(tenant)
	now := l.clock()

	b.mu.Lock()
	defer b.mu.Unlock()

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.params.Burst, b.tokens+elapsed*l.params.Rate)
		b.last = now
	}

	if b.tokens < 1 {
		missing := 1 - b.tokens
		wait := time.Duration(missing / l.params.Rate * float64(time.Second))
		return Decision{Allowed: false, Remaining: 0, RetryAfter: wait.Round(time.Millisecond)}
	}
	b.tokens--
	return Decision{Allowed: true, Remaining: math.Floor(b.tokens)}
}

func (l *Limiter) bucketFor(tenant string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[tenant]; ok {
		return b
	}
	b := &bucket{tokens: l.params.Burst, last: l.clock()}
	l.buckets[tenant] = b
	return b
}

// Len reports how many tenants currently hold a bucket.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Snapshot reports the remaining tokens per tenant, for the admin endpoint.
func (l *Limiter) Snapshot() map[string]float64 {
	claims := make(map[string]*bucket)
	l.mu.Lock()
	for tenant, b := range l.buckets {
		claims[tenant] = b
	}
	l.mu.Unlock()

	out := make(map[string]float64, len(claims))
	for tenant, b := range claims {
		b.mu.Lock()
		out[tenant] = math.Floor(b.tokens)
		b.mu.Unlock()
	}
	return out
}

// Sweep drops buckets that have been idle for longer than idle. It returns the
// number of evicted tenants.
func (l *Limiter) Sweep(idle time.Duration) int {
	cutoff := l.clock().Add(-idle)
	l.mu.Lock()
	defer l.mu.Unlock()

	n := 0
	for tenant, b := range l.buckets {
		b.mu.Lock()
		stale := b.last.Before(cutoff)
		b.mu.Unlock()
		if stale {
			delete(l.buckets, tenant)
			n++
		}
	}
	return n
}

// SweepEvery runs Sweep on an interval until done is closed, which makes bucket
// growth bounded by tenant churn rather than by process uptime.
func (l *Limiter) SweepEvery(interval, idle time.Duration, done <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.Sweep(idle)
		case <-done:
			return
		}
	}
}

// Package metrics counts limiter decisions per tenant and renders them in the
// Prometheus text exposition format, without pulling in a client library.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds allowed and rejected counters keyed by tenant. It is safe for
// concurrent use.
type Registry struct {
	mu     sync.Mutex
	series map[string]*counters
}

type counters struct {
	allowed  atomic.Uint64
	rejected atomic.Uint64
}

func New() *Registry {
	return &Registry{series: make(map[string]*counters)}
}

// Observe records one decision for tenant. It is meant to be installed as
// Limiter.OnDecision.
func (r *Registry) Observe(tenant string, allowed bool) {
	c := r.seriesFor(tenant)
	if allowed {
		c.allowed.Add(1)
		return
	}
	c.rejected.Add(1)
}

func (r *Registry) seriesFor(tenant string) *counters {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.series[tenant]; ok {
		return c
	}
	c := new(counters)
	r.series[tenant] = c
	return c
}

// WriteTo renders the counters in text format. Tenants are sorted so scraping
// twice without traffic yields byte-identical output.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	tenants := make([]string, 0, len(r.series))
	r.mu.Lock()
	for tenant := range r.series {
		tenants = append(tenants, tenant)
	}
	r.mu.Unlock()
	sort.Strings(tenants)

	var b strings.Builder
	b.WriteString("# HELP ratelimit_requests_total Rate limit decisions per tenant.\n")
	b.WriteString("# TYPE ratelimit_requests_total counter\n")
	for _, tenant := range tenants {
		c := r.seriesFor(tenant)
		label := escape(tenant)
		fmt.Fprintf(&b, "ratelimit_requests_total{tenant=%q,decision=\"allowed\"} %d\n", label, c.allowed.Load())
		fmt.Fprintf(&b, "ratelimit_requests_total{tenant=%q,decision=\"rejected\"} %d\n", label, c.rejected.Load())
	}

	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// escape applies the label escaping the text format requires.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

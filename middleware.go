package ratelimitd

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

// TenantHeader is the header DefaultKey reads a tenant from.
const TenantHeader = "X-Tenant-ID"

// KeyFunc extracts the tenant a request should be billed to. Returning an error
// makes the middleware fail open, so a broken identity layer cannot take the
// service down.
type KeyFunc func(*http.Request) (string, error)

// DefaultKey reads TenantHeader and rejects requests without it.
func DefaultKey(r *http.Request) (string, error) {
	tenant := r.Header.Get(TenantHeader)
	if tenant == "" {
		return "", ErrNoTenant
	}
	return tenant, nil
}

// Middleware rate limits requests by tenant. Allowed requests reach next with
// the remaining quota exposed in X-RateLimit-Remaining; rejected ones get 429,
// a Retry-After header and a short body.
func (l *Limiter) Middleware(key KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant, err := key(r)
			if err != nil {
				w.Header().Set("X-RateLimit-Policy", "fail-open")
				next.ServeHTTP(w, r)
				return
			}

			d := l.Allow(tenant)
			w.Header().Set("X-RateLimit-Limit", formatFloat(l.params.Burst))
			w.Header().Set("X-RateLimit-Remaining", formatFloat(d.Remaining))
			if !d.Allowed {
				w.Header().Set("Retry-After", formatRetryAfter(d.RetryAfter))
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("rate limit exceeded for tenant " + tenant + "\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// formatRetryAfter renders whole seconds, never below one, because Retry-After
// delays are documented in seconds.
func formatRetryAfter(d time.Duration) string {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

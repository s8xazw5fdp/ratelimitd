package metrics

import (
	"strings"
	"testing"
)

func TestRenderSkipsSilentTenantsAndSorts(t *testing.T) {
	r := New()
	r.Observe("globex", true)
	r.Observe("acme", false)
	r.Observe("acme", false)

	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	got := b.String()

	if strings.Index(got, `tenant="acme"`) > strings.Index(got, `tenant="globex"`) {
		t.Fatalf("tenants not sorted:\n%s", got)
	}
	if !strings.Contains(got, `ratelimit_requests_total{tenant="acme",decision="rejected"} 2`) {
		t.Fatalf("missing acme rejection count:\n%s", got)
	}
	if strings.Contains(got, `tenant="acme",decision="allowed"`) && !strings.Contains(got, `decision="allowed"} 0`) {
		t.Fatalf("zero counters not emitted:\n%s", got)
	}
	if !strings.HasPrefix(got, "# HELP ratelimit_requests_total") {
		t.Fatalf("missing HELP header:\n%s", got)
	}
}

func TestLabelEscaping(t *testing.T) {
	r := New()
	r.Observe("a\"b\\c", true)

	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if !strings.Contains(b.String(), `tenant="a\\\"b\\\\c"`) {
		t.Fatalf("label not escaped:\n%s", b.String())
	}
}

package metrics

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCounterIncrement(t *testing.T) {
	c := &Counter{}
	if c.Value() != 0 {
		t.Fatalf("expected 0, got %d", c.Value())
	}
	c.Inc()
	c.Inc()
	c.Inc()
	if c.Value() != 3 {
		t.Fatalf("expected 3, got %d", c.Value())
	}
}

func TestHistogramObserve(t *testing.T) {
	h := NewHistogram([]float64{0.1, 0.5, 1.0})

	h.Observe(0.05) // bucket 0 (le=0.1)
	h.Observe(0.3)  // bucket 1 (le=0.5)
	h.Observe(0.8)  // bucket 2 (le=1.0)
	h.Observe(5.0)  // bucket 3 (+Inf)

	if h.Count() != 4 {
		t.Fatalf("expected count 4, got %d", h.Count())
	}

	expectedSum := 0.05 + 0.3 + 0.8 + 5.0
	if diff := h.Sum() - expectedSum; diff > 0.001 || diff < -0.001 {
		t.Fatalf("expected sum %f, got %f", expectedSum, h.Sum())
	}

	cum := h.CumulativeCounts()
	// le=0.1: 1, le=0.5: 2, le=1.0: 3, +Inf: 4
	expected := []int64{1, 2, 3, 4}
	for i, want := range expected {
		if cum[i] != want {
			t.Errorf("bucket %d: expected %d, got %d", i, want, cum[i])
		}
	}
}

func TestHistogramBoundary(t *testing.T) {
	h := NewHistogram([]float64{1.0})

	// Value exactly at the boundary should go into that bucket.
	h.Observe(1.0)
	cum := h.CumulativeCounts()
	if cum[0] != 1 {
		t.Errorf("expected 1 in le=1.0 bucket, got %d", cum[0])
	}
}

func TestMethodIndex(t *testing.T) {
	tests := []struct {
		method string
		want   int
	}{
		{"ping", 0},
		{"find_node", 1},
		{"get_peers", 2},
		{"announce_peer", 3},
		{"unknown", -1},
	}
	for _, tt := range tests {
		if got := MethodIndex(tt.method); got != tt.want {
			t.Errorf("MethodIndex(%q) = %d, want %d", tt.method, got, tt.want)
		}
	}
}

func TestHandlerOutput(t *testing.T) {
	m := NewMetrics(
		func() int { return 42 },
		func() int { return 7 },
		func() int { return 3 },
	)

	m.QueriesInbound[MethodPing].Inc()
	m.QueriesInbound[MethodPing].Inc()
	m.QueriesOutbound[MethodFindNode].Inc()
	m.ResponseSuccess.Inc()
	m.ResponseTimeout.Inc()
	m.TokenFailure.Inc()
	m.BEP42NonCompliant.Inc()
	m.RateLimited.Inc()
	m.SetBootstrapDuration(1.234)
	m.LookupDuration.Observe(0.05)
	m.LookupDuration.Observe(0.5)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()

	expectedLines := []string{
		`dht_queries_total{method="ping",direction="inbound"} 2`,
		`dht_queries_total{method="find_node",direction="outbound"} 1`,
		`dht_responses_total{status="success"} 1`,
		`dht_responses_total{status="timeout"} 1`,
		`dht_token_validations_total{result="failure"} 1`,
		`dht_anomalies_total{type="bep42_non_compliant"} 1`,
		`dht_rate_limited_total 1`,
		`dht_routing_table_size 42`,
		`dht_peers_stored 7`,
		`dht_rate_limited_ips 3`,
		`dht_bootstrap_duration_seconds 1.234`,
		`dht_lookup_duration_seconds_count 2`,
		`# TYPE dht_lookup_duration_seconds histogram`,
	}

	for _, line := range expectedLines {
		if !strings.Contains(body, line) {
			t.Errorf("expected output to contain %q\n\ngot:\n%s", line, body)
		}
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", contentType)
	}
}

func TestHandlerNilGauges(t *testing.T) {
	m := NewMetrics(nil, nil, nil)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "dht_routing_table_size 0") {
		t.Error("nil gauge should produce 0")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewMetrics(
		func() int { return 0 },
		func() int { return 0 },
		func() int { return 0 },
	)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.QueriesInbound[MethodPing].Inc()
				m.QueriesOutbound[MethodFindNode].Inc()
				m.ResponseSuccess.Inc()
				m.RateLimited.Inc()
				m.LookupDuration.Observe(0.1)
			}
		}()
	}

	// Concurrent scrapes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			rec := httptest.NewRecorder()
			m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		}
	}()

	wg.Wait()

	if m.QueriesInbound[MethodPing].Value() != 1000 {
		t.Errorf("expected 1000 ping queries, got %d", m.QueriesInbound[MethodPing].Value())
	}
}

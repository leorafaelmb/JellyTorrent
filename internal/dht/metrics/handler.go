package metrics

import (
	"fmt"
	"net/http"
	"strconv"
)

// Handler returns an http.Handler that serves metrics in Prometheus text
// exposition format at any path (typically mounted at /metrics).
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.writeMetrics(w)
	})
}

func (m *Metrics) writeMetrics(w http.ResponseWriter) {
	// dht_queries_total
	fmt.Fprintln(w, "# HELP dht_queries_total Total DHT queries by method and direction.")
	fmt.Fprintln(w, "# TYPE dht_queries_total counter")
	for i := 0; i < 4; i++ {
		name := MethodName(i)
		fmt.Fprintf(w, "dht_queries_total{method=%q,direction=\"inbound\"} %d\n", name, m.QueriesInbound[i].Value())
		fmt.Fprintf(w, "dht_queries_total{method=%q,direction=\"outbound\"} %d\n", name, m.QueriesOutbound[i].Value())
	}

	// dht_responses_total
	fmt.Fprintln(w, "# HELP dht_responses_total Outgoing query response outcomes.")
	fmt.Fprintln(w, "# TYPE dht_responses_total counter")
	fmt.Fprintf(w, "dht_responses_total{status=\"success\"} %d\n", m.ResponseSuccess.Value())
	fmt.Fprintf(w, "dht_responses_total{status=\"timeout\"} %d\n", m.ResponseTimeout.Value())
	fmt.Fprintf(w, "dht_responses_total{status=\"ctx_canceled\"} %d\n", m.ResponseCanceled.Value())

	// dht_token_validations_total
	fmt.Fprintln(w, "# HELP dht_token_validations_total Announce peer token validation results.")
	fmt.Fprintln(w, "# TYPE dht_token_validations_total counter")
	fmt.Fprintf(w, "dht_token_validations_total{result=\"success\"} %d\n", m.TokenSuccess.Value())
	fmt.Fprintf(w, "dht_token_validations_total{result=\"failure\"} %d\n", m.TokenFailure.Value())

	// dht_anomalies_total
	fmt.Fprintln(w, "# HELP dht_anomalies_total Security anomaly events by type.")
	fmt.Fprintln(w, "# TYPE dht_anomalies_total counter")
	fmt.Fprintf(w, "dht_anomalies_total{type=\"bep42_non_compliant\"} %d\n", m.BEP42NonCompliant.Value())
	fmt.Fprintf(w, "dht_anomalies_total{type=\"rate_limited\"} %d\n", m.RateLimited.Value())

	// dht_rate_limited_total
	fmt.Fprintln(w, "# HELP dht_rate_limited_total Total queries dropped by rate limiter.")
	fmt.Fprintln(w, "# TYPE dht_rate_limited_total counter")
	fmt.Fprintf(w, "dht_rate_limited_total %d\n", m.RateLimited.Value())

	// dht_routing_table_size
	fmt.Fprintln(w, "# HELP dht_routing_table_size Current number of nodes in the routing table.")
	fmt.Fprintln(w, "# TYPE dht_routing_table_size gauge")
	tableSize := 0
	if m.RoutingTableSize != nil {
		tableSize = m.RoutingTableSize()
	}
	fmt.Fprintf(w, "dht_routing_table_size %d\n", tableSize)

	// dht_peers_stored
	fmt.Fprintln(w, "# HELP dht_peers_stored Current number of stored peer entries.")
	fmt.Fprintln(w, "# TYPE dht_peers_stored gauge")
	peersStored := 0
	if m.PeersStored != nil {
		peersStored = m.PeersStored()
	}
	fmt.Fprintf(w, "dht_peers_stored %d\n", peersStored)

	// dht_rate_limited_ips
	fmt.Fprintln(w, "# HELP dht_rate_limited_ips Current number of IPs tracked by the rate limiter.")
	fmt.Fprintln(w, "# TYPE dht_rate_limited_ips gauge")
	rateLimitedIPs := 0
	if m.RateLimitedIPs != nil {
		rateLimitedIPs = m.RateLimitedIPs()
	}
	fmt.Fprintf(w, "dht_rate_limited_ips %d\n", rateLimitedIPs)

	// dht_bootstrap_duration_seconds
	fmt.Fprintln(w, "# HELP dht_bootstrap_duration_seconds Duration of the most recent bootstrap in seconds.")
	fmt.Fprintln(w, "# TYPE dht_bootstrap_duration_seconds gauge")
	fmt.Fprintf(w, "dht_bootstrap_duration_seconds %s\n", formatFloat(m.GetBootstrapDuration()))

	// dht_lookup_duration_seconds
	fmt.Fprintln(w, "# HELP dht_lookup_duration_seconds Distribution of iterative lookup durations.")
	fmt.Fprintln(w, "# TYPE dht_lookup_duration_seconds histogram")
	cumCounts := m.LookupDuration.CumulativeCounts()
	bounds := m.LookupDuration.Bounds()
	for i, b := range bounds {
		fmt.Fprintf(w, "dht_lookup_duration_seconds_bucket{le=%q} %d\n", formatFloat(b), cumCounts[i])
	}
	fmt.Fprintf(w, "dht_lookup_duration_seconds_bucket{le=\"+Inf\"} %d\n", cumCounts[len(bounds)])
	fmt.Fprintf(w, "dht_lookup_duration_seconds_sum %s\n", formatFloat(m.LookupDuration.Sum()))
	fmt.Fprintf(w, "dht_lookup_duration_seconds_count %d\n", m.LookupDuration.Count())
}

// formatFloat formats a float64 without trailing zeros.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

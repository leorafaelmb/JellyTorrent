package metrics

import (
	"math"
	"sync/atomic"
)

// Counter is a monotonically increasing integer, safe for concurrent use.
type Counter struct {
	v atomic.Int64
}

func (c *Counter) Inc()         { c.v.Add(1) }
func (c *Counter) Value() int64 { return c.v.Load() }

// Histogram tracks the distribution of observed values across fixed buckets.
// Thread-safe via atomic operations.
type Histogram struct {
	bounds  []float64      // upper bounds (exclusive), e.g. 0.01, 0.05, ...
	counts  []atomic.Int64 // one counter per bucket
	count   atomic.Int64   // total observations
	sumBits atomic.Uint64  // float64 sum stored as bits
}

func NewHistogram(bounds []float64) *Histogram {
	return &Histogram{
		bounds: bounds,
		counts: make([]atomic.Int64, len(bounds)+1), // +1 for +Inf bucket
	}
}

func (h *Histogram) Observe(v float64) {
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i].Add(1)
			h.count.Add(1)
			h.addSum(v)
			return
		}
	}
	// Falls in +Inf bucket
	h.counts[len(h.bounds)].Add(1)
	h.count.Add(1)
	h.addSum(v)
}

func (h *Histogram) addSum(v float64) {
	for {
		old := h.sumBits.Load()
		new := math.Float64bits(math.Float64frombits(old) + v)
		if h.sumBits.CompareAndSwap(old, new) {
			return
		}
	}
}

func (h *Histogram) Count() int64   { return h.count.Load() }
func (h *Histogram) Sum() float64   { return math.Float64frombits(h.sumBits.Load()) }
func (h *Histogram) Bounds() []float64 { return h.bounds }

// CumulativeCounts returns cumulative bucket counts (each bucket includes
// all observations <= its upper bound).
func (h *Histogram) CumulativeCounts() []int64 {
	result := make([]int64, len(h.bounds)+1)
	var cumulative int64
	for i := range result {
		cumulative += h.counts[i].Load()
		result[i] = cumulative
	}
	return result
}

// Method index constants for the QueriesInbound/QueriesOutbound arrays.
const (
	MethodPing         = 0
	MethodFindNode     = 1
	MethodGetPeers     = 2
	MethodAnnouncePeer = 3
)

var methodNames = [4]string{"ping", "find_node", "get_peers", "announce_peer"}

// MethodIndex returns the array index for a KRPC method name.
// Returns -1 for unknown methods.
func MethodIndex(method string) int {
	switch method {
	case "ping":
		return MethodPing
	case "find_node":
		return MethodFindNode
	case "get_peers":
		return MethodGetPeers
	case "announce_peer":
		return MethodAnnouncePeer
	default:
		return -1
	}
}

// MethodName returns the method name for a given index.
func MethodName(i int) string {
	if i < 0 || i >= len(methodNames) {
		return "unknown"
	}
	return methodNames[i]
}

// Metrics holds all Prometheus-style metrics for the DHT node.
type Metrics struct {
	// Counters by method (indexed by MethodPing..MethodAnnouncePeer)
	QueriesInbound  [4]*Counter
	QueriesOutbound [4]*Counter

	// Response outcome counters
	ResponseSuccess  *Counter
	ResponseTimeout  *Counter
	ResponseCanceled *Counter

	// Token validation counters
	TokenSuccess *Counter
	TokenFailure *Counter

	// Anomaly counters
	BEP42NonCompliant *Counter
	RateLimited       *Counter

	// Gauges — functions called at scrape time
	RoutingTableSize func() int
	PeersStored      func() int
	RateLimitedIPs   func() int

	// Bootstrap duration — stored as float64 bits (seconds)
	BootstrapDuration atomic.Uint64

	// Lookup duration histogram
	LookupDuration *Histogram
}

// NewMetrics creates a Metrics instance with gauge functions wired in.
// Pass nil for any gauge function that isn't available.
func NewMetrics(tableSize, peersStored, rateLimitedIPs func() int) *Metrics {
	m := &Metrics{
		ResponseSuccess:  &Counter{},
		ResponseTimeout:  &Counter{},
		ResponseCanceled: &Counter{},
		TokenSuccess:     &Counter{},
		TokenFailure:     &Counter{},
		BEP42NonCompliant: &Counter{},
		RateLimited:      &Counter{},
		RoutingTableSize: tableSize,
		PeersStored:      peersStored,
		RateLimitedIPs:   rateLimitedIPs,
		LookupDuration: NewHistogram([]float64{
			0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10,
		}),
	}
	for i := range m.QueriesInbound {
		m.QueriesInbound[i] = &Counter{}
		m.QueriesOutbound[i] = &Counter{}
	}
	return m
}

// SetBootstrapDuration stores the duration of the most recent bootstrap.
func (m *Metrics) SetBootstrapDuration(seconds float64) {
	m.BootstrapDuration.Store(math.Float64bits(seconds))
}

// GetBootstrapDuration returns the duration of the most recent bootstrap.
func (m *Metrics) GetBootstrapDuration() float64 {
	return math.Float64frombits(m.BootstrapDuration.Load())
}

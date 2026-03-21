package hardening

import (
	"net/netip"
	"sync"
	"time"
)

// ipWindow holds the sliding window counter state for a single IP.
type ipWindow struct {
	prevCount int
	currCount int
	currStart time.Time
}

// RateLimiter enforces per-IP query rate limits using a sliding window
// counter. Two fixed windows (previous + current) are maintained per IP,
// and the current rate is estimated as:
//
//	prevCount * (window - elapsed) / window + currCount
//
// This avoids the boundary-burst problem of fixed windows while using
// O(1) memory per IP.
type RateLimiter struct {
	windows map[netip.Addr]*ipWindow
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxIPs  int
}

// NewRateLimiter creates a rate limiter that allows at most limit queries
// per window per IP address. maxIPs caps the number of tracked IPs to
// prevent memory exhaustion from spoofed source addresses.
func NewRateLimiter(limit int, window time.Duration, maxIPs int) *RateLimiter {
	if limit <= 0 {
		limit = 50
	}
	if window <= 0 {
		window = time.Minute
	}
	if maxIPs <= 0 {
		maxIPs = 100000
	}
	return &RateLimiter{
		windows: make(map[netip.Addr]*ipWindow),
		limit:   limit,
		window:  window,
		maxIPs:  maxIPs,
	}
}

// Allow checks whether a query from ip should be allowed. It increments
// the counter and returns false if the estimated rate exceeds the limit.
func (rl *RateLimiter) Allow(ip netip.Addr) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	w, ok := rl.windows[ip]
	if !ok {
		w = &ipWindow{currStart: now}
		rl.windows[ip] = w
		if len(rl.windows) > rl.maxIPs {
			rl.cleanupLocked(now)
		}
	}

	elapsed := now.Sub(w.currStart)

	// Both windows fully expired — reset.
	if elapsed >= 2*rl.window {
		w.prevCount = 0
		w.currCount = 0
		w.currStart = now
		elapsed = 0
	} else if elapsed >= rl.window {
		// Current window expired — rotate.
		w.prevCount = w.currCount
		w.currCount = 0
		w.currStart = w.currStart.Add(rl.window)
		elapsed = now.Sub(w.currStart)
	}

	w.currCount++

	// Sliding window estimate: weight previous window by remaining overlap.
	remaining := rl.window - elapsed
	estimate := w.prevCount*int(remaining)/int(rl.window) + w.currCount

	return estimate <= rl.limit
}

// Cleanup removes entries for IPs that have been inactive for at least
// two full windows. Returns the number of entries removed.
// Safe to call from a separate goroutine.
func (rl *RateLimiter) Cleanup() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.cleanupLocked(time.Now())
}

// cleanupLocked sweeps the map and deletes fully expired entries.
// Must be called with rl.mu held. Returns the number of entries removed.
func (rl *RateLimiter) cleanupLocked(now time.Time) int {
	removed := 0
	cutoff := 2 * rl.window
	for ip, w := range rl.windows {
		if now.Sub(w.currStart) >= cutoff {
			delete(rl.windows, ip)
			removed++
		}
	}
	return removed
}

// Len returns the number of currently tracked IPs.
func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.windows)
}

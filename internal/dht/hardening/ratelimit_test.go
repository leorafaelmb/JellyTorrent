package hardening

import (
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestRateLimiterAllowUnderLimit(t *testing.T) {
	rl := NewRateLimiter(10, time.Second, 1000)
	ip := netip.MustParseAddr("1.2.3.4")

	for i := 0; i < 10; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("query %d should be allowed (limit 10)", i+1)
		}
	}
}

func TestRateLimiterDenyOverLimit(t *testing.T) {
	rl := NewRateLimiter(5, time.Second, 1000)
	ip := netip.MustParseAddr("1.2.3.4")

	for i := 0; i < 5; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("query %d should be allowed (limit 5)", i+1)
		}
	}

	if rl.Allow(ip) {
		t.Error("query 6 should be denied (limit 5)")
	}
	if rl.Allow(ip) {
		t.Error("query 7 should be denied (limit 5)")
	}
}

func TestRateLimiterWindowRotation(t *testing.T) {
	window := 50 * time.Millisecond
	rl := NewRateLimiter(5, window, 1000)
	ip := netip.MustParseAddr("1.2.3.4")

	// Exhaust the limit.
	for i := 0; i < 5; i++ {
		rl.Allow(ip)
	}
	if rl.Allow(ip) {
		t.Error("should be denied after exhausting limit")
	}

	// Wait for both windows to fully expire.
	time.Sleep(2*window + 10*time.Millisecond)

	// Should be allowed again.
	if !rl.Allow(ip) {
		t.Error("should be allowed after full window expiration")
	}
}

func TestRateLimiterSlidingWindowEstimate(t *testing.T) {
	// Use a long enough window to avoid timing flakiness.
	window := 100 * time.Millisecond
	rl := NewRateLimiter(10, window, 1000)
	ip := netip.MustParseAddr("1.2.3.4")

	// Fill the first window with exactly 10 queries.
	for i := 0; i < 10; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("query %d in first window should be allowed", i+1)
		}
	}

	// Wait for the window to rotate but NOT fully expire.
	// At the midpoint of window 2, the previous window contributes ~50%
	// of its count (5), so we should have ~5 slots left.
	time.Sleep(window + window/2)

	// We should be able to send some queries but not 10.
	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow(ip) {
			allowed++
		}
	}

	// Previous window contributes ~5 (10 * 50%), so we expect ~5 allowed.
	// Allow some slack for timing: between 3 and 7.
	if allowed < 3 || allowed > 7 {
		t.Errorf("expected ~5 queries allowed at midpoint, got %d", allowed)
	}
}

func TestRateLimiterIndependentIPs(t *testing.T) {
	rl := NewRateLimiter(3, time.Second, 1000)
	ip1 := netip.MustParseAddr("1.2.3.4")
	ip2 := netip.MustParseAddr("5.6.7.8")

	// Exhaust IP1's limit.
	for i := 0; i < 3; i++ {
		rl.Allow(ip1)
	}
	if rl.Allow(ip1) {
		t.Error("IP1 should be denied")
	}

	// IP2 should still be allowed.
	if !rl.Allow(ip2) {
		t.Error("IP2 should be allowed (independent counter)")
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	window := 20 * time.Millisecond
	rl := NewRateLimiter(100, window, 1000)
	ip := netip.MustParseAddr("1.2.3.4")

	rl.Allow(ip)
	if rl.Len() != 1 {
		t.Fatalf("expected 1 tracked IP, got %d", rl.Len())
	}

	// Wait for full expiration (2 windows).
	time.Sleep(2*window + 10*time.Millisecond)

	rl.Cleanup()
	if rl.Len() != 0 {
		t.Errorf("expected 0 tracked IPs after cleanup, got %d", rl.Len())
	}
}

func TestRateLimiterCleanupKeepsFresh(t *testing.T) {
	window := 20 * time.Millisecond
	rl := NewRateLimiter(100, window, 1000)
	fresh := netip.MustParseAddr("1.2.3.4")
	stale := netip.MustParseAddr("5.6.7.8")

	rl.Allow(stale)
	time.Sleep(2*window + 10*time.Millisecond)
	rl.Allow(fresh)

	rl.Cleanup()
	if rl.Len() != 1 {
		t.Errorf("expected 1 tracked IP (fresh only), got %d", rl.Len())
	}
}

func TestRateLimiterMaxIPsCap(t *testing.T) {
	rl := NewRateLimiter(100, time.Hour, 10) // tiny cap for testing

	// Insert 15 IPs. The emergency cleanup won't remove any because
	// none are expired (window is 1 hour), but it should not panic.
	for i := 0; i < 15; i++ {
		ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})
		rl.Allow(ip)
	}

	if rl.Len() > 15 {
		t.Errorf("unexpected map size: %d", rl.Len())
	}

	// Now test that expired entries ARE cleaned up by the cap trigger.
	rl2 := NewRateLimiter(100, 10*time.Millisecond, 5)
	for i := 0; i < 5; i++ {
		ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})
		rl2.Allow(ip)
	}
	time.Sleep(30 * time.Millisecond) // let entries expire

	// This insert should trigger emergency cleanup of the 5 expired entries.
	newIP := netip.AddrFrom4([4]byte{10, 0, 0, 99})
	rl2.Allow(newIP)

	if rl2.Len() != 1 {
		t.Errorf("expected 1 tracked IP after cap-triggered cleanup, got %d", rl2.Len())
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(1000, time.Second, 10000)
	var wg sync.WaitGroup

	// Hammer Allow() from multiple goroutines.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(n)})
			for j := 0; j < 100; j++ {
				rl.Allow(ip)
			}
		}(i)
	}

	// Hammer Cleanup() concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			rl.Cleanup()
		}
	}()

	wg.Wait()
}

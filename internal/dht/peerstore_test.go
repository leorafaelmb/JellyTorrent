package dht

import (
	"net/netip"
	"testing"
	"time"
)

func TestPeerStoreAddGet(t *testing.T) {
	ps := NewPeerStore(30 * time.Minute)
	ih := [20]byte{1}
	addr := netip.MustParseAddrPort("192.168.1.1:6881")

	ps.Add(ih, addr)
	got := ps.Get(ih)
	if len(got) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(got))
	}
	if got[0] != addr {
		t.Errorf("got %v, want %v", got[0], addr)
	}
}

func TestPeerStoreGetEmpty(t *testing.T) {
	ps := NewPeerStore(30 * time.Minute)
	got := ps.Get([20]byte{0xff})
	if got != nil {
		t.Errorf("expected nil for unknown infohash, got %v", got)
	}
}

func TestPeerStoreTTLExpiration(t *testing.T) {
	ps := NewPeerStore(1 * time.Millisecond)
	ih := [20]byte{2}
	addr := netip.MustParseAddrPort("10.0.0.1:6881")

	ps.Add(ih, addr)
	time.Sleep(5 * time.Millisecond)

	got := ps.Get(ih)
	if len(got) != 0 {
		t.Fatalf("expired peer should not be returned, got %d peers", len(got))
	}
}

func TestPeerStoreDuplicateUpdate(t *testing.T) {
	ps := NewPeerStore(30 * time.Minute)
	ih := [20]byte{3}
	addr := netip.MustParseAddrPort("10.0.0.1:6881")

	ps.Add(ih, addr)
	ps.Add(ih, addr)

	got := ps.Get(ih)
	if len(got) != 1 {
		t.Fatalf("duplicate add should not create second entry, got %d peers", len(got))
	}
}

func TestPeerStoreMultiplePeers(t *testing.T) {
	ps := NewPeerStore(30 * time.Minute)
	ih := [20]byte{4}
	a1 := netip.MustParseAddrPort("10.0.0.1:6881")
	a2 := netip.MustParseAddrPort("10.0.0.2:6882")

	ps.Add(ih, a1)
	ps.Add(ih, a2)

	got := ps.Get(ih)
	if len(got) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(got))
	}
}

func TestPeerStoreMultipleInfohashes(t *testing.T) {
	ps := NewPeerStore(30 * time.Minute)
	ih1 := [20]byte{5}
	ih2 := [20]byte{6}
	addr := netip.MustParseAddrPort("10.0.0.1:6881")

	ps.Add(ih1, addr)
	ps.Add(ih2, addr)

	if len(ps.Get(ih1)) != 1 || len(ps.Get(ih2)) != 1 {
		t.Error("each infohash should have its own peer list")
	}
}

func TestPeerStoreExpireCleanup(t *testing.T) {
	ps := NewPeerStore(1 * time.Millisecond)
	ih := [20]byte{7}
	addr := netip.MustParseAddrPort("10.0.0.1:6881")

	ps.Add(ih, addr)
	time.Sleep(5 * time.Millisecond)

	ps.Expire()

	// After Expire(), the entry should be fully removed
	ps.mu.RLock()
	_, exists := ps.peers[ih]
	ps.mu.RUnlock()
	if exists {
		t.Error("Expire should delete empty infohash entries")
	}
}

func TestPeerStoreExpireKeepsFresh(t *testing.T) {
	ps := NewPeerStore(1 * time.Hour)
	ih := [20]byte{8}
	addr := netip.MustParseAddrPort("10.0.0.1:6881")

	ps.Add(ih, addr)
	ps.Expire()

	got := ps.Get(ih)
	if len(got) != 1 {
		t.Error("Expire should keep non-expired peers")
	}
}

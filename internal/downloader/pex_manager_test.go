package downloader

import (
	"net/netip"
	"testing"

	"github.com/leorafaelmb/JellyTorrent/internal/peer"
)

func TestPEXManagerPeerConnectDisconnect(t *testing.T) {
	pm := NewPEXManager(nil)

	addr1 := netip.MustParseAddrPort("1.2.3.4:6881")
	addr2 := netip.MustParseAddrPort("5.6.7.8:6882")

	pm.PeerConnected(addr1)
	pm.PeerConnected(addr2)

	delta := pm.ComputeDelta("9.9.9.9:1234")

	if len(delta.Added) != 2 {
		t.Fatalf("expected 2 added, got %d", len(delta.Added))
	}

	// Second call should have no changes
	delta = pm.ComputeDelta("9.9.9.9:1234")
	if len(delta.Added) != 0 || len(delta.Dropped) != 0 {
		t.Fatalf("expected no changes, got added=%d dropped=%d", len(delta.Added), len(delta.Dropped))
	}

	// Disconnect addr1
	pm.PeerDisconnected(addr1)

	delta = pm.ComputeDelta("9.9.9.9:1234")
	// Should not report addr1 as dropped since we only have addr2 now
	// and addr1 was in lastSent but is no longer connected
	// Wait — we need to re-check. The lastSent for "9.9.9.9:1234" was set to {addr1, addr2}.
	// But PeerDisconnected also cleans up lastSent for the disconnected peer.
	// Actually PeerDisconnected only cleans up lastSent[addr.String()], not entries in other peers' lastSent.
	// So lastSent["9.9.9.9:1234"] still has {addr1, addr2}. Current connected = {addr2}.
	// Delta: dropped = {addr1}
	if len(delta.Dropped) != 1 {
		t.Fatalf("expected 1 dropped, got %d", len(delta.Dropped))
	}
}

func TestPEXManagerExcludesSelf(t *testing.T) {
	pm := NewPEXManager(nil)

	addr1 := netip.MustParseAddrPort("1.2.3.4:6881")
	addr2 := netip.MustParseAddrPort("5.6.7.8:6882")

	pm.PeerConnected(addr1)
	pm.PeerConnected(addr2)

	// ComputeDelta for addr1 should not include addr1 itself
	delta := pm.ComputeDelta(addr1.String())

	if len(delta.Added) != 1 {
		t.Fatalf("expected 1 added (excluding self), got %d", len(delta.Added))
	}
	if delta.Added[0] != addr2 {
		t.Errorf("expected %s, got %s", addr2, delta.Added[0])
	}
}

func TestPEXManagerHandlePEX(t *testing.T) {
	initialPeers := []netip.AddrPort{
		netip.MustParseAddrPort("1.2.3.4:6881"),
	}
	pm := NewPEXManager(initialPeers)

	msg := &peer.PEXMessage{
		Added: []netip.AddrPort{
			netip.MustParseAddrPort("1.2.3.4:6881"), // already known
			netip.MustParseAddrPort("9.9.9.9:1234"), // new
		},
	}

	pm.HandlePEX(msg)

	// Only the new peer should be in the discovered channel
	select {
	case addr := <-pm.Discovered():
		if addr != netip.MustParseAddrPort("9.9.9.9:1234") {
			t.Errorf("expected 9.9.9.9:1234, got %s", addr)
		}
	default:
		t.Fatal("expected a discovered peer")
	}

	// Channel should be empty now
	select {
	case addr := <-pm.Discovered():
		t.Fatalf("unexpected extra peer: %s", addr)
	default:
		// good
	}
}

func TestPEXManagerHandlePEXDedup(t *testing.T) {
	pm := NewPEXManager(nil)

	addr := netip.MustParseAddrPort("9.9.9.9:1234")
	msg := &peer.PEXMessage{Added: []netip.AddrPort{addr}}

	pm.HandlePEX(msg)
	pm.HandlePEX(msg) // same peer again

	// Should only appear once
	count := 0
	for {
		select {
		case <-pm.Discovered():
			count++
		default:
			goto done
		}
	}
done:
	if count != 1 {
		t.Fatalf("expected 1 discovered peer (dedup), got %d", count)
	}
}

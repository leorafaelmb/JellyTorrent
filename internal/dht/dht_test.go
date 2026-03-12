package dht

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

func newTestDHT(t *testing.T) *DHT {
	t.Helper()
	d, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
	)
	if err != nil {
		t.Fatalf("failed to create DHT: %v", err)
	}
	return d
}

func localAddr(d *DHT) string {
	ap := d.server.LocalAddr()
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), ap.Port()).String()
}

func TestDHTBootstrapTwoNodes(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)
	defer b.Close()

	b.config.BootstrapNodes = []string{localAddr(a)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := b.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if b.table.NumNodes() == 0 {
		t.Error("B's routing table should have at least one node after bootstrapping from A")
	}
	if a.table.NumNodes() == 0 {
		t.Error("A's routing table should have at least one node after B bootstrapped")
	}
}

func TestDHTThreeNodeDiscovery(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)
	defer b.Close()
	c := newTestDHT(t)
	defer c.Close()

	aAddr := localAddr(a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.config.BootstrapNodes = []string{aAddr}
	b.Bootstrap(ctx)

	c.config.BootstrapNodes = []string{aAddr}
	c.Bootstrap(ctx)

	time.Sleep(200 * time.Millisecond)

	if a.table.NumNodes() < 2 {
		t.Errorf("A should know at least 2 nodes, has %d", a.table.NumNodes())
	}
}

func TestDHTAnnouncePeerGetPeers(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.config.BootstrapNodes = []string{localAddr(a)}
	b.Bootstrap(ctx)
	time.Sleep(100 * time.Millisecond)

	infoHash := [20]byte{0xAA, 0xBB, 0xCC}

	err := a.Announce(ctx, infoHash, 9999)
	if err != nil {
		t.Fatalf("announce failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	peers, err := b.GetPeers(ctx, infoHash)
	if err != nil {
		t.Fatalf("get_peers failed: %v", err)
	}

	if len(peers) == 0 {
		localPeers := b.peers.Get(infoHash)
		aPeers := a.peers.Get(infoHash)
		if len(localPeers) == 0 && len(aPeers) == 0 {
			t.Error("no peers found for infohash after announce — neither node stored the peer")
		}
	}

	aPeers := a.peers.Get(infoHash)
	bPeers := b.peers.Get(infoHash)
	if len(aPeers)+len(bPeers) == 0 {
		t.Error("at least one node should have stored the announced peer")
	}
}

func TestDHTGetPeersReturnsStoredPeers(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.config.BootstrapNodes = []string{localAddr(a)}
	b.Bootstrap(ctx)
	time.Sleep(100 * time.Millisecond)

	infoHash := [20]byte{0xDE, 0xAD}
	peerAddr := netip.MustParseAddrPort("1.2.3.4:5678")
	a.peers.Add(infoHash, peerAddr)

	peers, err := b.GetPeers(ctx, infoHash)
	if err != nil {
		t.Fatalf("get_peers failed: %v", err)
	}

	found := false
	for _, p := range peers {
		if p == peerAddr {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find peer %v, got %v", peerAddr, peers)
	}
}

func TestDHTCloseIsClean(t *testing.T) {
	d := newTestDHT(t)
	err := d.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

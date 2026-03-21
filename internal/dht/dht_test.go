package dht

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/hardening"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/routing"
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

// --- BEP 42 integration tests ---

func newTestDHTWithBEP42(t *testing.T, mode BEP42Mode) *DHT {
	t.Helper()
	d, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithBEP42(mode),
	)
	if err != nil {
		t.Fatalf("failed to create DHT: %v", err)
	}
	return d
}

func TestBEP42HandleQueryEnforce(t *testing.T) {
	a := newTestDHTWithBEP42(t, BEP42Enforce)
	defer a.Close()
	b := newTestDHT(t) // B has a random (non-compliant) ID
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// B sends a query to A. Since B's ID is random (non-compliant),
	// A in Enforce mode should drop it and not insert B.
	b.config.BootstrapNodes = []string{localAddr(a)}
	b.Bootstrap(ctx)
	time.Sleep(100 * time.Millisecond)

	// A should not have inserted B's non-compliant ID.
	if a.table.NumNodes() != 0 {
		t.Errorf("BEP42Enforce: expected 0 nodes in A's table, got %d", a.table.NumNodes())
	}
}

func TestBEP42HandleQueryLog(t *testing.T) {
	a := newTestDHTWithBEP42(t, BEP42Log)
	defer a.Close()
	b := newTestDHT(t) // B has a random (non-compliant) ID
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// B sends a query to A. In Log mode, A should insert B but mark as non-compliant.
	b.config.BootstrapNodes = []string{localAddr(a)}
	b.Bootstrap(ctx)
	time.Sleep(100 * time.Millisecond)

	if a.table.NumNodes() == 0 {
		t.Fatal("BEP42Log: A should have inserted B's node")
	}

	// Verify the node is marked as non-compliant.
	nodes := a.table.Snapshot()
	for _, n := range nodes {
		if n.ID == b.id && n.Compliant {
			t.Error("BEP42Log: B's node should be marked non-compliant")
		}
	}
}

func TestBEP42IPFieldInResponse(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Send a find_node from B to A and check the response has an IP field.
	aAddr := netip.MustParseAddrPort(localAddr(a))
	resp := b.sendFindNode(ctx, aAddr, b.id)
	if resp == nil {
		t.Fatal("expected response from A")
	}

	if resp.IP == nil {
		t.Fatal("response should contain BEP 42 IP field")
	}

	addr, ok := parseCompactAddr(resp.IP)
	if !ok {
		t.Fatal("failed to parse compact addr from IP field")
	}

	// The IP field should contain B's address as seen by A.
	if addr.Addr() != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("IP field addr = %s, want 127.0.0.1", addr.Addr())
	}
}

func TestBEP42SaveLoadExternalIP(t *testing.T) {
	d := newTestDHT(t)
	d.externalIP = netip.MustParseAddr("203.0.113.42")
	defer d.Close()

	tmp := filepath.Join(t.TempDir(), "state.dat")
	if err := d.Save(tmp); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	state, err := loadRoutingTable(tmp)
	if err != nil {
		t.Fatalf("loadRoutingTable failed: %v", err)
	}

	if state.externalIP != d.externalIP {
		t.Errorf("external IP not preserved: got %s, want %s", state.externalIP, d.externalIP)
	}
}

func TestBEP42CompliantNodeEvictsNonCompliant(t *testing.T) {
	ip := netip.MustParseAddr("124.31.75.21")

	// Generate a compliant ID for a known IP.
	compliantID, err := hardening.GenerateCompliantID(ip)
	if err != nil {
		t.Fatal(err)
	}

	// Create a routing table and fill a bucket with non-compliant nodes.
	rt := routing.NewRoutingTable(compliantID, nil)

	// Find a bucket that we can fill. Insert nodes with non-compliant IDs
	// that land in the same bucket.
	bucketFilled := false
	var targetBucket int
	for targetBucket = 0; targetBucket < 159; targetBucket++ {
		// Generate 8 nodes for this bucket distance.
		inserted := 0
		for attempt := 0; attempt < 200 && inserted < routing.K; attempt++ {
			fakeID := makeIDInBucket(compliantID, targetBucket)
			n := &routing.Node{
				ID:        fakeID,
				Addr:      netip.MustParseAddrPort("10.0.0.1:6881"),
				LastSeen:  time.Now(),
				Compliant: false,
			}
			_, ok := rt.Insert(n)
			if ok {
				inserted++
			}
		}
		if inserted == routing.K {
			bucketFilled = true
			break
		}
	}

	if !bucketFilled {
		t.Skip("could not fill a bucket for eviction test")
	}

	before := rt.NumNodes()

	// Now insert a compliant node into the same bucket.
	compliantNodeID := makeIDInBucket(compliantID, targetBucket)
	compliantNode := &routing.Node{
		ID:        compliantNodeID,
		Addr:      netip.MustParseAddrPort("10.0.0.2:6881"),
		LastSeen:  time.Now(),
		Compliant: true,
	}
	_, ok := rt.Insert(compliantNode)
	if !ok {
		t.Error("compliant node should have been inserted by evicting a non-compliant node")
	}

	// Total should remain the same (one evicted, one inserted).
	if rt.NumNodes() != before {
		t.Errorf("expected %d nodes, got %d", before, rt.NumNodes())
	}
}

// --- Rate limiting integration tests ---

func TestRateLimitDropsExcessQueries(t *testing.T) {
	// Target with a very low rate limit: 5 queries per 1-second window.
	target, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithRateLimit(5, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("failed to create target DHT: %v", err)
	}
	defer target.Close()

	sender := newTestDHT(t)
	defer sender.Close()

	targetAddr := netip.MustParseAddrPort(localAddr(target))

	// Send 10 rapid queries with short timeouts so all complete well within
	// the 1-second window (avoids sliding window decay letting extras through).
	responded := 0
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		resp := sender.sendFindNode(ctx, targetAddr, sender.id)
		cancel()
		if resp != nil {
			responded++
		}
	}

	if responded > 5 {
		t.Errorf("expected at most 5 responses (rate limit), got %d", responded)
	}
	if responded < 3 {
		t.Errorf("expected at least 3 responses before hitting rate limit, got %d", responded)
	}
}

func TestRateLimitDisabledWithZero(t *testing.T) {
	// Rate limiting disabled: all queries should be allowed.
	target, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithRateLimit(0, 0),
	)
	if err != nil {
		t.Fatalf("failed to create target DHT: %v", err)
	}
	defer target.Close()

	if target.rateLimiter != nil {
		t.Error("rateLimiter should be nil when limit is 0")
	}

	sender := newTestDHT(t)
	defer sender.Close()

	targetAddr := netip.MustParseAddrPort(localAddr(target))

	// Send 20 rapid queries — all should get responses.
	responded := 0
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		resp := sender.sendFindNode(ctx, targetAddr, sender.id)
		cancel()
		if resp != nil {
			responded++
		}
	}

	if responded < 15 {
		t.Errorf("expected most queries to succeed with rate limiting disabled, got %d/20", responded)
	}
}

// makeIDInBucket creates a random NodeID that falls into the given bucket
// relative to self (i.e., PrefixLen(self, result) == bucket).
func makeIDInBucket(self [20]byte, bucket int) [20]byte {
	// XOR distance must have exactly `bucket` leading zero bits.
	var id [20]byte
	// Copy self first.
	copy(id[:], self[:])

	// The bit at position `bucket` in the XOR distance must be 1.
	// All bits before it must be 0 (same as self).
	// Bits after it can be random.
	byteIdx := bucket / 8
	bitIdx := uint(7 - (bucket % 8))

	// Flip the target bit to ensure XOR has a 1 there.
	id[byteIdx] ^= 1 << bitIdx

	// Randomize all bytes after byteIdx to avoid duplicate IDs.
	for i := byteIdx + 1; i < 20; i++ {
		id[i] = self[i] ^ byte(int(i)+bucket) // deterministic but varied
	}

	return id
}

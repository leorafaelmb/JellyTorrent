package routing

import (
	"net/netip"
	"testing"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

func makeTableNode(id nodeid.NodeID) *Node {
	return &Node{
		ID:       id,
		Addr:     netip.MustParseAddrPort("127.0.0.1:6881"),
		LastSeen: time.Now(),
	}
}

func TestTableInsertAndNumNodes(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	n := makeTableNode(nodeid.NodeID{0x80}) // prefix len 0 from self
	_, ok := rt.Insert(n)
	if !ok {
		t.Fatal("insert should succeed")
	}
	if rt.NumNodes() != 1 {
		t.Fatalf("expected 1 node, got %d", rt.NumNodes())
	}
}

func TestTableInsertCorrectBucket(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	// Nodes with different prefix lengths should go to different buckets
	n1 := makeTableNode(nodeid.NodeID{0x80}) // prefix len 0
	n2 := makeTableNode(nodeid.NodeID{0x40}) // prefix len 1
	n3 := makeTableNode(nodeid.NodeID{0x01}) // prefix len 7
	rt.Insert(n1)
	rt.Insert(n2)
	rt.Insert(n3)

	if rt.NumNodes() != 3 {
		t.Fatalf("expected 3 nodes, got %d", rt.NumNodes())
	}
}

func TestTableFindClosestSorted(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	ids := []nodeid.NodeID{
		{0x80},
		{0x40},
		{0x20},
		{0x10},
		{0x08},
	}
	for _, id := range ids {
		rt.Insert(makeTableNode(id))
	}

	target := nodeid.NodeID{0x09}
	closest := rt.FindClosest(target, 3)
	if len(closest) != 3 {
		t.Fatalf("expected 3 closest nodes, got %d", len(closest))
	}

	// Verify sorted by distance to target
	for i := 1; i < len(closest); i++ {
		d1 := target.Distance(closest[i-1].ID)
		d2 := target.Distance(closest[i].ID)
		if d2.Less(d1) {
			t.Errorf("results not sorted by distance at index %d", i)
		}
	}
}

func TestTableFindClosestReturnsCorrectNodes(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	// Insert nodes at various distances from target 0x10
	ids := []nodeid.NodeID{
		{0x10}, // distance 0x00 to target (closest)
		{0x11}, // distance 0x01
		{0x13}, // distance 0x03
		{0x80}, // distance 0x90 (far)
		{0xff}, // distance 0xef (far)
	}
	for _, id := range ids {
		rt.Insert(makeTableNode(id))
	}

	target := nodeid.NodeID{0x10}
	closest := rt.FindClosest(target, 3)
	if len(closest) != 3 {
		t.Fatalf("expected 3, got %d", len(closest))
	}

	// The 3 closest should be 0x10, 0x11, 0x13
	expected := map[byte]bool{0x10: true, 0x11: true, 0x13: true}
	for _, n := range closest {
		if !expected[n.ID[0]] {
			t.Errorf("unexpected node %x in closest results", n.ID[0])
		}
	}
}

func TestTableFindClosestFewerThanCount(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	rt.Insert(makeTableNode(nodeid.NodeID{0x80}))
	rt.Insert(makeTableNode(nodeid.NodeID{0x40}))

	closest := rt.FindClosest(nodeid.NodeID{0x80}, 10)
	if len(closest) != 2 {
		t.Fatalf("expected 2 nodes (all we have), got %d", len(closest))
	}
}

func TestTableFindClosestEmpty(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	closest := rt.FindClosest(nodeid.NodeID{0x80}, 5)
	if len(closest) != 0 {
		t.Fatalf("expected 0 results from empty table, got %d", len(closest))
	}
}

func TestTablePrefixLen160Boundary(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	// Inserting a node with the same ID as self (PrefixLen=160) should
	// be silently rejected, not panic.
	sameNode := makeTableNode(self)
	_, ok := rt.Insert(sameNode)
	if ok {
		t.Error("inserting self should return false")
	}
	if rt.NumNodes() != 0 {
		t.Error("self should not be added to the routing table")
	}
}

func TestTableSnapshot(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	ids := []nodeid.NodeID{{0x80}, {0x40}, {0x20}, {0x10}, {0x08}}
	for _, id := range ids {
		rt.Insert(makeTableNode(id))
	}

	snap := rt.Snapshot()
	if len(snap) != len(ids) {
		t.Fatalf("expected %d nodes in snapshot, got %d", len(ids), len(snap))
	}

	found := make(map[byte]bool)
	for _, n := range snap {
		found[n.ID[0]] = true
	}
	for _, id := range ids {
		if !found[id[0]] {
			t.Errorf("snapshot missing node %x", id[0])
		}
	}
}

func TestTableSnapshotEmpty(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)

	snap := rt.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected 0 nodes in empty snapshot, got %d", len(snap))
	}
}

func TestTableSelf(t *testing.T) {
	self := nodeid.NodeID{0xAB, 0xCD}
	rt := NewRoutingTable(self)

	if rt.Self() != self {
		t.Errorf("Self() returned %x, want %x", rt.Self(), self)
	}
}

func TestTableRemove(t *testing.T) {
	self := nodeid.NodeID{}
	rt := NewRoutingTable(self)
	n := makeTableNode(nodeid.NodeID{0x80})
	rt.Insert(n)

	ok := rt.Remove(n)
	if !ok {
		t.Error("remove should return true for existing node")
	}
	if rt.NumNodes() != 0 {
		t.Error("table should be empty after remove")
	}
}

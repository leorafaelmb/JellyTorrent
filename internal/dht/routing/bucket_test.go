package routing

import (
	"net/netip"
	"testing"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

func makeNode(id byte) *Node {
	nid := nodeid.NodeID{id}
	return &Node{
		ID:       nid,
		Addr:     netip.MustParseAddrPort("127.0.0.1:6881"),
		LastSeen: time.Now(),
	}
}

func TestBucketInsertAndLen(t *testing.T) {
	b := NewBucket()
	n := makeNode(1)
	_, ok := b.Insert(n)
	if !ok {
		t.Fatal("insert should succeed into empty bucket")
	}
	if b.Len() != 1 {
		t.Fatalf("expected len 1, got %d", b.Len())
	}
}

func TestBucketLRUOrdering(t *testing.T) {
	b := NewBucket()
	n1 := makeNode(1)
	n2 := makeNode(2)
	n3 := makeNode(3)
	b.Insert(n1)
	b.Insert(n2)
	b.Insert(n3)

	if b.Oldest() != n1 {
		t.Error("oldest should be first inserted node")
	}

	// Re-insert n1 to move to tail
	b.Insert(n1)
	if b.Oldest() != n2 {
		t.Error("after re-insert, oldest should be n2")
	}

	nodes := b.Nodes()
	if nodes[len(nodes)-1] != n1 {
		t.Error("re-inserted node should be at tail")
	}
}

func TestBucketFullRejectsNew(t *testing.T) {
	b := NewBucket()
	for i := byte(0); i < K; i++ {
		b.Insert(makeNode(i))
	}
	if b.Len() != K {
		t.Fatalf("expected %d nodes, got %d", K, b.Len())
	}

	oldest, ok := b.Insert(makeNode(0xff))
	if ok {
		t.Error("full bucket should reject new node")
	}
	if oldest == nil {
		t.Error("should return oldest node when full")
	}
	if oldest.ID[0] != 0 {
		t.Errorf("oldest should be node 0, got %d", oldest.ID[0])
	}
	if b.Len() != K {
		t.Error("bucket size should not change after rejection")
	}
}

func TestBucketFullAcceptsExisting(t *testing.T) {
	b := NewBucket()
	for i := byte(0); i < K; i++ {
		b.Insert(makeNode(i))
	}

	// Re-inserting existing node should succeed even when full
	_, ok := b.Insert(makeNode(3))
	if !ok {
		t.Error("re-inserting existing node into full bucket should succeed")
	}
	if b.Len() != K {
		t.Error("bucket size should not change after re-insert")
	}
	// Node 3 should now be at tail
	nodes := b.Nodes()
	if nodes[len(nodes)-1].ID[0] != 3 {
		t.Error("re-inserted node should be at tail")
	}
}

func TestBucketDuplicate(t *testing.T) {
	b := NewBucket()
	n := makeNode(1)
	b.Insert(n)
	b.Insert(n)
	if b.Len() != 1 {
		t.Fatalf("duplicate insert should not increase length, got %d", b.Len())
	}
}

func TestBucketRemove(t *testing.T) {
	b := NewBucket()
	n := makeNode(1)
	b.Insert(n)

	ok := b.Remove(n.ID)
	if !ok {
		t.Error("remove of existing node should return true")
	}
	if b.Len() != 0 {
		t.Error("bucket should be empty after remove")
	}

	ok = b.Remove(n.ID)
	if ok {
		t.Error("remove of non-existent node should return false")
	}
}

func TestBucketOldestEmpty(t *testing.T) {
	b := NewBucket()
	if b.Oldest() != nil {
		t.Error("oldest of empty bucket should be nil")
	}
}

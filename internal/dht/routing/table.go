package routing

import (
	"crypto/rand"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

func formatAddr(addr netip.AddrPort) string {
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()).String()
}

type RoutingTable struct {
	self    nodeid.NodeID
	buckets [160]*Bucket
	mu      sync.RWMutex
	logger  *slog.Logger
}

func NewRoutingTable(self nodeid.NodeID, logger *slog.Logger) *RoutingTable {
	if logger == nil {
		logger = slog.Default()
	}
	buckets := [160]*Bucket{}

	for i := range buckets {
		buckets[i] = NewBucket()
	}

	return &RoutingTable{
		self:    self,
		buckets: buckets,
		mu:      sync.RWMutex{},
		logger:  logger,
	}

}

func (rt *RoutingTable) Insert(node *Node) (*Node, bool) {
	i := rt.self.PrefixLen(node.ID)
	if i >= 160 {
		return nil, false // don't insert self
	}
	rt.mu.Lock()
	wasFull := rt.buckets[i].Len() == K
	result, success := rt.buckets[i].Insert(node)
	tableSize := rt.countLocked()
	rt.mu.Unlock()

	if success && wasFull && result == nil {
		// Bucket was full but insertion succeeded — either a refresh (existing node
		// moved to tail) or a BEP 42 compliant eviction. We can't distinguish these
		// without more info from the bucket, but compliant eviction is the interesting
		// case and only happens when node.Compliant is true.
		if node.Compliant {
			rt.logger.Debug("routing table node evicted",
				"new_id", node.ID.String(),
				"new_addr", formatAddr(node.Addr),
				"bucket", i,
				"table_size", tableSize,
			)
		}
	} else if success && !wasFull {
		rt.logger.Debug("routing table node added",
			"node_id", node.ID.String(),
			"addr", formatAddr(node.Addr),
			"bucket", i,
			"table_size", tableSize,
		)
	} else if !success {
		rt.logger.Debug("routing table insert rejected",
			"node_id", node.ID.String(),
			"addr", formatAddr(node.Addr),
			"bucket", i,
			"table_size", tableSize,
		)
	}

	return result, success
}

func (rt *RoutingTable) Remove(node *Node) bool {
	i := rt.self.PrefixLen(node.ID)
	if i >= 160 {
		return false
	}
	rt.mu.Lock()
	ok := rt.buckets[i].Remove(node.ID)
	rt.mu.Unlock()
	if ok {
		rt.logger.Debug("routing table node removed",
			"node_id", node.ID.String(),
			"bucket", i,
		)
	}
	return ok
}

func (rt *RoutingTable) FindClosest(target nodeid.NodeID, count int) []*Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// gather candidates from nearby buckets
	var candidates []*Node
	i := rt.self.PrefixLen(target)
	if i >= 160 {
		i = 159
	}

	// start at target bucket, expand outward
	candidates = append(candidates, rt.buckets[i].Nodes()...)
	l, r := i-1, i+1
	for len(candidates) < count && (l >= 0 || r < 160) {
		if l >= 0 {
			candidates = append(candidates, rt.buckets[l].Nodes()...)
			l--
		}
		if r < 160 {
			candidates = append(candidates, rt.buckets[r].Nodes()...)
			r++
		}
	}

	// sort by XOR distance to target
	sort.Slice(candidates, func(a, b int) bool {
		distA := target.Distance(candidates[a].ID)
		distB := target.Distance(candidates[b].ID)
		return distA.Less(distB)
	})

	// trim to count
	if len(candidates) > count {
		candidates = candidates[:count]
	}
	return candidates
}

// Snapshot returns a copy of all nodes in the routing table.
func (rt *RoutingTable) Snapshot() []*Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var nodes []*Node
	for _, b := range rt.buckets {
		nodes = append(nodes, b.Nodes()...)
	}
	return nodes
}

// Self returns the node ID of the local node.
func (rt *RoutingTable) Self() nodeid.NodeID {
	return rt.self
}

// countLocked returns the total number of nodes. Must be called with rt.mu held.
func (rt *RoutingTable) countLocked() int {
	n := 0
	for _, b := range rt.buckets {
		n += b.Len()
	}
	return n
}

// RecordFailure increments the failure count for a node. If the node
// reaches MaxFailures consecutive failures, it is evicted. Returns true
// if the node was evicted.
func (rt *RoutingTable) RecordFailure(id nodeid.NodeID) bool {
	i := rt.self.PrefixLen(id)
	if i >= 160 {
		return false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, n := range rt.buckets[i].Nodes() {
		if n.ID == id {
			n.FailCount++
			if n.FailCount >= MaxFailures {
				rt.buckets[i].Remove(id)
				rt.logger.Debug("stale node evicted",
					"node_id", id.String(),
					"addr", formatAddr(n.Addr),
					"bucket", i,
					"fail_count", n.FailCount,
					"table_size", rt.countLocked(),
				)
				return true
			}
			return false
		}
	}
	return false
}

// RecordSuccess resets the failure count for a node and updates its
// LastSeen timestamp.
func (rt *RoutingTable) RecordSuccess(id nodeid.NodeID) {
	i := rt.self.PrefixLen(id)
	if i >= 160 {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, n := range rt.buckets[i].Nodes() {
		if n.ID == id {
			n.FailCount = 0
			n.LastSeen = time.Now()
			return
		}
	}
}

// Stale returns all nodes whose LastSeen is older than the given threshold.
func (rt *RoutingTable) Stale(threshold time.Duration) []*Node {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var stale []*Node
	cutoff := time.Now().Add(-threshold)
	for _, b := range rt.buckets {
		for _, n := range b.Nodes() {
			if n.LastSeen.Before(cutoff) {
				stale = append(stale, n)
			}
		}
	}
	return stale
}

// StaleBuckets returns the indices of non-empty buckets where all nodes
// have a LastSeen older than the given threshold.
func (rt *RoutingTable) StaleBuckets(threshold time.Duration) []int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	cutoff := time.Now().Add(-threshold)
	var stale []int
	for i, b := range rt.buckets {
		nodes := b.Nodes()
		if len(nodes) == 0 {
			continue
		}
		allStale := true
		for _, n := range nodes {
			if n.LastSeen.After(cutoff) {
				allStale = false
				break
			}
		}
		if allStale {
			stale = append(stale, i)
		}
	}
	return stale
}

// TargetForBucket generates a random node ID that falls in the given bucket
// relative to this routing table's self ID (i.e., PrefixLen(self, target) == bucket).
func (rt *RoutingTable) TargetForBucket(bucket int) nodeid.NodeID {
	var target nodeid.NodeID
	copy(target[:], rt.self[:])

	// Flip the bit at position `bucket` to ensure XOR distance has a 1 there.
	byteIdx := bucket / 8
	bitIdx := uint(7 - (bucket % 8))
	target[byteIdx] ^= 1 << bitIdx

	// Randomize bytes after the target byte to avoid identical targets.
	var rnd [20]byte
	rand.Read(rnd[:])
	for i := byteIdx + 1; i < 20; i++ {
		target[i] = rt.self[i] ^ rnd[i]
	}

	return target
}

func (rt *RoutingTable) NumNodes() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	numNodes := 0
	for _, b := range rt.buckets {
		numNodes += b.Len()
	}

	return numNodes
}

package routing

import (
	"log/slog"
	"net/netip"
	"sort"
	"sync"

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

func (rt *RoutingTable) NumNodes() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	numNodes := 0
	for _, b := range rt.buckets {
		numNodes += b.Len()
	}

	return numNodes
}

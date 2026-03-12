package routing

import (
	"sort"
	"sync"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

type RoutingTable struct {
	self    nodeid.NodeID
	buckets [160]*Bucket
	mu      sync.RWMutex
}

func NewRoutingTable(self nodeid.NodeID) *RoutingTable {
	buckets := [160]*Bucket{}

	for i := range buckets {
		buckets[i] = NewBucket()
	}

	return &RoutingTable{
		self:    self,
		buckets: buckets,
		mu:      sync.RWMutex{},
	}

}

func (rt *RoutingTable) Insert(node *Node) (*Node, bool) {
	i := rt.self.PrefixLen(node.ID)
	if i >= 160 {
		return nil, false // don't insert self
	}
	rt.mu.Lock()
	node, success := rt.buckets[i].Insert(node)
	rt.mu.Unlock()
	return node, success
}

func (rt *RoutingTable) Remove(node *Node) bool {
	i := rt.self.PrefixLen(node.ID)
	if i >= 160 {
		return false
	}
	rt.mu.Lock()
	ok := rt.buckets[i].Remove(node.ID)
	rt.mu.Unlock()
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

func (rt *RoutingTable) NumNodes() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	numNodes := 0
	for _, b := range rt.buckets {
		numNodes += b.Len()
	}

	return numNodes
}

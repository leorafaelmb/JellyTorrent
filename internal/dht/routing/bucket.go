package routing

import (
	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

type Bucket struct {
	nodes []*Node
}

const K = 8

func NewBucket() *Bucket {
	b := &Bucket{}
	b.nodes = make([]*Node, 0, K)
	return b
}

func (b *Bucket) Insert(node *Node) (*Node, bool) {
	if len(b.nodes) == K {
		for i, n := range b.nodes {
			// node already exists, move to tail
			if n.ID == node.ID {
				b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
				b.nodes = append(b.nodes, node)
				return nil, true
			}
		}
		// BEP 42: if the new node is compliant, evict the oldest non-compliant
		// node to prefer verified nodes. When BEP 42 is off, all nodes have
		// Compliant=false, so this branch never triggers.
		if node.Compliant {
			for i, n := range b.nodes {
				if !n.Compliant {
					b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
					b.nodes = append(b.nodes, node)
					return nil, true
				}
			}
		}
		// return oldest node, and false to indicate this node hasn't been added
		return b.nodes[0], false
	} else {

		for i, n := range b.nodes {
			if n.ID == node.ID {
				b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
				b.nodes = append(b.nodes, node)
				return nil, true
			}
		}
		b.nodes = append(b.nodes, node)
		return nil, true
	}

}

func (b *Bucket) Remove(id nodeid.NodeID) bool {
	for i, n := range b.nodes {
		if n.ID == id {
			b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
			return true
		}
	}
	return false
}

func (b *Bucket) Oldest() *Node {
	if b.nodes == nil || len(b.nodes) == 0 {
		return nil
	}
	return b.nodes[0]
}

func (b *Bucket) Nodes() []*Node {
	return b.nodes
}

func (b *Bucket) Len() int {
	return len(b.nodes)

}

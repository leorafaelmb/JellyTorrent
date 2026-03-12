package nodeid

import (
	"crypto/rand"
	"fmt"
	"math/bits"
)

type NodeID [20]byte

func New() NodeID {
	id := NodeID{}
	rand.Read(id[:])
	return id
}

func FromBytes(data []byte) (NodeID, error) {
	if data == nil || len(data) != 20 {
		return NodeID{}, fmt.Errorf("invalid length: data should be 20 bytes")
	}
	var id NodeID
	copy(id[:], data)
	return id, nil
}

func (n NodeID) Distance(other NodeID) NodeID {
	dist := NodeID{}
	for i, b := range n {
		dist[i] = b ^ other[i]
	}
	return dist
}

func (n NodeID) PrefixLen(other NodeID) int {
	dist := n.Distance(other)
	count := 0
	for _, b := range dist {
		if b == 0 {
			count += 8
		} else {
			count += bits.LeadingZeros8(b)
			break
		}
	}
	return count
}

// Less returns true if n is less than other. Used for node distances.
func (n NodeID) Less(other NodeID) bool {
	for i, b := range n {
		if b != other[i] {
			return b < other[i]
		}
	}
	return false
}

func (n NodeID) String() string {
	return fmt.Sprintf("%x", n[:])
}

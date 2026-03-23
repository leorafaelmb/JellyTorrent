package routing

import (
	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
	"net/netip"
	"time"
)

// MaxFailures is the number of consecutive query failures before a node
// is evicted from the routing table.
const MaxFailures = 5

type Node struct {
	ID        nodeid.NodeID
	Addr      netip.AddrPort
	LastSeen  time.Time
	Compliant bool // BEP 42: node ID derives from IP
	FailCount int  // consecutive query failures
}

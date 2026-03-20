package routing

import (
	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
	"net/netip"
	"time"
)

type Node struct {
	ID        nodeid.NodeID
	Addr      netip.AddrPort
	LastSeen  time.Time
	Compliant bool // BEP 42: node ID derives from IP
}

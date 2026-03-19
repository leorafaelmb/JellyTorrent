package downloader

import (
	"net/netip"
	"sync"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
)

// PEXManager tracks connected peers and coordinates PEX message exchange (BEP 11).
// It computes per-peer deltas (added/dropped since last PEX send) and provides
// a channel of newly discovered peer addresses from incoming PEX messages.
type PEXManager struct {
	mu         sync.Mutex
	connected  map[string]netip.AddrPort  // currently connected peers (addr string → AddrPort)
	lastSent   map[string]map[string]bool // per peer: set of addrs we told them about last time
	discovered chan netip.AddrPort         // new peers from incoming PEX messages
	seen       map[string]bool            // all peers ever seen (dedup for discovered channel)
}

// NewPEXManager creates a PEX manager with an initial set of known peer addresses.
func NewPEXManager(initialPeers []netip.AddrPort) *PEXManager {
	seen := make(map[string]bool, len(initialPeers))
	for _, addr := range initialPeers {
		seen[addr.String()] = true
	}
	return &PEXManager{
		connected:  make(map[string]netip.AddrPort),
		lastSent:   make(map[string]map[string]bool),
		discovered: make(chan netip.AddrPort, 256),
		seen:       seen,
	}
}

// PeerConnected records that a peer has connected.
func (pm *PEXManager) PeerConnected(addr netip.AddrPort) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.connected[addr.String()] = addr
}

// PeerDisconnected records that a peer has disconnected.
func (pm *PEXManager) PeerDisconnected(addr netip.AddrPort) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	key := addr.String()
	delete(pm.connected, key)
	delete(pm.lastSent, key)
}

// HandlePEX processes an incoming PEX message. New (unseen) peers are sent
// to the discovered channel for the downloader to spawn workers for.
func (pm *PEXManager) HandlePEX(msg *peer.PEXMessage) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, addr := range msg.Added {
		key := addr.String()
		if pm.seen[key] {
			continue
		}
		pm.seen[key] = true
		select {
		case pm.discovered <- addr:
			logger.Log.Debug("PEX discovered new peer", "addr", addr)
		default:
			// channel full, drop
		}
	}
}

// ComputeDelta returns a PEX message with the peers added/dropped since the last
// PEX message sent to peerAddr. Updates the lastSent state for the peer.
func (pm *PEXManager) ComputeDelta(peerAddr string) *peer.PEXMessage {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	prev := pm.lastSent[peerAddr]
	if prev == nil {
		prev = make(map[string]bool)
	}

	// Current connected set excluding the peer we're sending to
	current := make(map[string]bool, len(pm.connected))
	for key := range pm.connected {
		if key != peerAddr {
			current[key] = true
		}
	}

	// Compute added: in current but not in prev
	var added []netip.AddrPort
	for key := range current {
		if !prev[key] && len(added) < internal.PEXMaxAdded {
			added = append(added, pm.connected[key])
		}
	}

	// Compute dropped: in prev but not in current
	var dropped []netip.AddrPort
	for key := range prev {
		if !current[key] {
			if addr, ok := pm.connected[key]; ok {
				dropped = append(dropped, addr)
			} else {
				// peer is gone, parse from key
				if addr, err := netip.ParseAddrPort(key); err == nil {
					dropped = append(dropped, addr)
				}
			}
		}
	}

	// Update lastSent
	pm.lastSent[peerAddr] = current

	// Flags: all zeros (no encryption/uTP/holepunch info)
	addedF := make([]byte, len(added))

	return &peer.PEXMessage{
		Added:   added,
		AddedF:  addedF,
		Dropped: dropped,
	}
}

// Discovered returns the channel of newly discovered peer addresses from incoming PEX messages.
func (pm *PEXManager) Discovered() <-chan netip.AddrPort {
	return pm.discovered
}

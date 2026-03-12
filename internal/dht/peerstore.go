package dht

import (
	"net/netip"
	"sync"
	"time"
)

type peer struct {
	Addr  netip.AddrPort
	Added time.Time
}

type PeerStore struct {
	peers map[[20]byte][]peer // infohash -> peers
	ttl   time.Duration       // 30 min default
	mu    sync.RWMutex
}

func NewPeerStore(ttl time.Duration) *PeerStore {
	return &PeerStore{
		peers: make(map[[20]byte][]peer),
		ttl:   ttl,
		mu:    sync.RWMutex{},
	}
}

func (ps *PeerStore) Add(infoHash [20]byte, addr netip.AddrPort) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, p := range ps.peers[infoHash] {
		if p.Addr == addr {
			ps.peers[infoHash][i].Added = time.Now()
			return
		}
	}
	newPeer := peer{Addr: addr, Added: time.Now()}
	ps.peers[infoHash] = append(ps.peers[infoHash], newPeer)

}

func (ps *PeerStore) Get(infoHash [20]byte) []netip.AddrPort {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	peers, ok := ps.peers[infoHash]
	if !ok {
		return nil
	}

	addrSlice := make([]netip.AddrPort, 0, len(peers))
	for _, p := range peers {
		if time.Since(p.Added) < ps.ttl {
			addrSlice = append(addrSlice, p.Addr)
		}
	}
	return addrSlice

}

func (ps *PeerStore) Expire() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for k, v := range ps.peers {
		kept := v[:0]
		for _, p := range v {
			if time.Since(p.Added) < ps.ttl {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			delete(ps.peers, k)
		} else {
			ps.peers[k] = kept
		}
	}
}

package seeder

import (
	"context"
	"math/rand"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/logger"
)

// Choker implements a simple round-robin unchoke algorithm.
// It periodically selects up to unchokeSlots interested peers to unchoke,
// shuffling for fairness.
type Choker struct {
	unchokeSlots int
	interval     time.Duration
}

// NewChoker creates a Choker with the given number of unchoke slots and rotation interval.
func NewChoker(slots int, interval time.Duration) *Choker {
	return &Choker{
		unchokeSlots: slots,
		interval:     interval,
	}
}

// Run starts the periodic unchoke rotation. Blocks until ctx is cancelled.
func (c *Choker) Run(ctx context.Context, getPeers func() []*SeedPeer) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run once immediately so peers get unchoked without waiting for the first tick
	c.tick(getPeers())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(getPeers())
		}
	}
}

func (c *Choker) tick(peers []*SeedPeer) {
	var interested []*SeedPeer
	for _, sp := range peers {
		if sp.interested {
			interested = append(interested, sp)
		}
	}

	if len(interested) == 0 {
		return
	}

	rand.Shuffle(len(interested), func(i, j int) {
		interested[i], interested[j] = interested[j], interested[i]
	})

	limit := c.unchokeSlots
	if limit > len(interested) {
		limit = len(interested)
	}

	toUnchoke := make(map[*SeedPeer]bool, limit)
	for i := 0; i < limit; i++ {
		toUnchoke[interested[i]] = true
	}

	for _, sp := range peers {
		shouldUnchoke := toUnchoke[sp]
		if shouldUnchoke && sp.peer.AmChoking {
			sp.peer.AmChoking = false
			if err := sp.sendUnchoke(); err != nil {
				logger.Log.Debug("error sending unchoke", "peer", sp.peer.Conn.RemoteAddr(), "error", err)
			}
		} else if !shouldUnchoke && !sp.peer.AmChoking {
			sp.peer.AmChoking = true
			if err := sp.sendChoke(); err != nil {
				logger.Log.Debug("error sending choke", "peer", sp.peer.Conn.RemoteAddr(), "error", err)
			}
		}
	}

	logger.Log.Debug("choker tick", "interested", len(interested), "unchoked", limit)
}

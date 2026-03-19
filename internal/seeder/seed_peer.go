package seeder

import (
	"context"
	"sync"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
)

// SeedPeer manages a single incoming peer connection for seeding.
type SeedPeer struct {
	peer       *peer.Peer
	interested bool
	writeMu    sync.Mutex // protects concurrent writes from serve loop and choker
}

// serve is the main message loop for an accepted seed connection.
// It reads messages and dispatches them until the context is cancelled or the peer disconnects.
func (sp *SeedPeer) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := sp.peer.ReadMessage()
		if err != nil {
			return err
		}

		if msg.IsKeepAlive() {
			continue
		}

		switch msg.ID {
		case internal.MessageInterested:
			sp.interested = true
			logger.Log.Debug("peer interested", "peer", sp.peer.Conn.RemoteAddr())

		case internal.MessageNotInterested:
			sp.interested = false
			logger.Log.Debug("peer not interested", "peer", sp.peer.Conn.RemoteAddr())

		case internal.MessageRequest:
			sp.writeMu.Lock()
			err := sp.peer.HandleRequest(msg.Payload)
			sp.writeMu.Unlock()
			if err != nil {
				logger.Log.Debug("error serving request", "peer", sp.peer.Conn.RemoteAddr(), "error", err)
			}

		case internal.MessageHave:
			if len(msg.Payload) >= 4 {
				sp.peer.BitField.SetPiece(int(msg.Payload[0])<<24 | int(msg.Payload[1])<<16 | int(msg.Payload[2])<<8 | int(msg.Payload[3]))
			}

		case internal.MessageCancel:
			// HandleRequest is synchronous, so cancels are no-ops

		case internal.MessageExtension:
			sp.peer.HandleExtension(msg.Payload)

		default:
			logger.Log.Debug("ignoring message from seed peer", "id", msg.ID, "peer", sp.peer.Conn.RemoteAddr())
		}
	}
}

// sendChoke sends a Choke message, acquiring the write lock.
func (sp *SeedPeer) sendChoke() error {
	sp.writeMu.Lock()
	defer sp.writeMu.Unlock()
	return sp.peer.SendChoke()
}

// sendUnchoke sends an Unchoke message, acquiring the write lock.
func (sp *SeedPeer) sendUnchoke() error {
	sp.writeMu.Lock()
	defer sp.writeMu.Unlock()
	return sp.peer.SendUnchoke()
}

// sendHave sends a Have message, acquiring the write lock.
func (sp *SeedPeer) sendHave(index uint32) error {
	sp.writeMu.Lock()
	defer sp.writeMu.Unlock()
	return sp.peer.SendHave(index)
}

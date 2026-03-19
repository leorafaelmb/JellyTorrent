package seeder

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
)

// Seeder listens for incoming BitTorrent peer connections and serves piece data.
type Seeder struct {
	config      Config
	infoHash    [20]byte
	blockServer peer.BlockServer
	bitfield    peer.BitField
	numPieces   int

	listener net.Listener

	mu    sync.Mutex
	peers map[string]*SeedPeer

	haveCh chan int // optional, for broadcasting new pieces to connected peers

	wg sync.WaitGroup
}

// New creates a Seeder. The blockServer provides piece data, and bitfield represents
// which pieces we have. haveCh is optional (nil for pure seeding after download).
func New(infoHash [20]byte, numPieces int, blockServer peer.BlockServer,
	bitfield peer.BitField, haveCh chan int, opts ...Option) *Seeder {

	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &Seeder{
		config:      cfg,
		infoHash:    infoHash,
		blockServer: blockServer,
		bitfield:    bitfield,
		numPieces:   numPieces,
		peers:       make(map[string]*SeedPeer),
		haveCh:      haveCh,
	}
}

// Run starts the TCP listener and accept loop. Blocks until ctx is cancelled.
func (s *Seeder) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.config.ListenPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = ln

	logger.Log.Info("seeder listening", "addr", ln.Addr().String())

	// Start the choker
	choker := NewChoker(s.config.UnchokeSlots, s.config.UnchokeInterval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		choker.Run(ctx, s.getPeers)
	}()

	// Start Have broadcaster if we have a channel
	if s.haveCh != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.broadcastHaves(ctx)
		}()
	}

	// Accept loop
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop(ctx)
	}()

	<-ctx.Done()
	ln.Close()

	// Close all peer connections to unblock serve goroutines
	s.mu.Lock()
	for addr, sp := range s.peers {
		sp.peer.Conn.Close()
		delete(s.peers, addr)
	}
	s.mu.Unlock()

	s.wg.Wait()

	logger.Log.Info("seeder stopped")
	return nil
}

// ListenPort returns the actual port the seeder is listening on.
func (s *Seeder) ListenPort() int {
	if s.listener == nil {
		return s.config.ListenPort
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *Seeder) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				logger.Log.Debug("accept error", "error", err)
				continue
			}
		}

		s.mu.Lock()
		peerCount := len(s.peers)
		s.mu.Unlock()

		if peerCount >= s.config.MaxPeers {
			logger.Log.Debug("max peers reached, rejecting connection", "addr", conn.RemoteAddr())
			conn.Close()
			continue
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Seeder) handleConn(ctx context.Context, conn net.Conn) {
	addr := conn.RemoteAddr().String()
	defer conn.Close()

	// Set deadline for handshake
	conn.SetDeadline(internal.HandshakeDeadline())

	p, h, err := peer.ServerHandshake(conn, s.infoHash)
	if err != nil {
		logger.Log.Debug("incoming handshake failed", "addr", addr, "error", err)
		return
	}

	// Reject self-connections
	if string(p.ID[:]) == internal.PeerID {
		logger.Log.Debug("rejecting self-connection", "addr", addr)
		return
	}

	// Clear the handshake deadline
	conn.SetDeadline(internal.ZeroDeadline())

	// Send our bitfield
	if err := p.SendBitfield(s.bitfield); err != nil {
		logger.Log.Debug("failed to send bitfield", "addr", addr, "error", err)
		return
	}

	// Extension handshake (BEP 10) — negotiate PEX if peer supports extensions
	if h.Reserved[internal.ExtensionBitPosition]&internal.ExtensionID != 0 {
		extResp, extErr := p.ExtensionHandshake()
		if extErr != nil {
			logger.Log.Debug("extension handshake failed (seed)", "addr", addr, "error", extErr)
		} else if extResp.UtPexID > 0 {
			p.UtPexID = extResp.UtPexID
			logger.Log.Debug("PEX negotiated (seed)", "addr", addr, "remoteUtPexID", extResp.UtPexID)
		}
	}

	// Set up upload support
	p.BlockServer = s.blockServer

	sp := &SeedPeer{peer: p}

	s.addPeer(addr, sp)
	defer s.removePeer(addr)

	logger.Log.Info("seed peer connected", "addr", addr)

	// Start PEX send goroutine if peer supports it
	if p.UtPexID > 0 {
		go s.pexSendLoop(ctx, sp)
	}

	if err := sp.serve(ctx); err != nil {
		select {
		case <-ctx.Done():
		default:
			logger.Log.Debug("seed peer disconnected", "addr", addr, "error", err)
		}
	}
}

func (s *Seeder) addPeer(addr string, sp *SeedPeer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[addr] = sp
}

func (s *Seeder) removePeer(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, addr)
}

func (s *Seeder) getPeers() []*SeedPeer {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*SeedPeer, 0, len(s.peers))
	for _, sp := range s.peers {
		result = append(result, sp)
	}
	return result
}

// pexSendLoop periodically sends PEX messages to a connected seed peer.
func (s *Seeder) pexSendLoop(ctx context.Context, sp *SeedPeer) {
	ticker := time.NewTicker(internal.PEXInterval)
	defer ticker.Stop()

	var lastSent map[string]bool

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := s.getPeerAddrs(sp.peer.Conn.RemoteAddr().String())

			// Compute delta
			var added []netip.AddrPort
			for key, addr := range current {
				if !lastSent[key] && len(added) < internal.PEXMaxAdded {
					added = append(added, addr)
				}
			}
			var dropped []netip.AddrPort
			for key := range lastSent {
				if _, ok := current[key]; !ok {
					if addr, err := netip.ParseAddrPort(key); err == nil {
						dropped = append(dropped, addr)
					}
				}
			}

			if len(added) == 0 && len(dropped) == 0 {
				continue
			}

			msg := &peer.PEXMessage{
				Added:   added,
				AddedF:  make([]byte, len(added)),
				Dropped: dropped,
			}

			sp.writeMu.Lock()
			err := sp.peer.SendPEX(msg)
			sp.writeMu.Unlock()
			if err != nil {
				logger.Log.Debug("failed to send PEX (seed)", "peer", sp.peer.Conn.RemoteAddr(), "error", err)
				return
			}

			// Update lastSent for next delta
			lastSent = make(map[string]bool, len(current))
			for key := range current {
				lastSent[key] = true
			}
		}
	}
}

// getPeerAddrs returns a snapshot of connected peer addresses, excluding excludeAddr.
func (s *Seeder) getPeerAddrs(excludeAddr string) map[string]netip.AddrPort {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[string]netip.AddrPort, len(s.peers))
	for addr, sp := range s.peers {
		if addr == excludeAddr {
			continue
		}
		result[addr] = sp.peer.AddrPort
	}
	return result
}

// broadcastHaves reads from haveCh and fans out Have messages to all connected peers.
func (s *Seeder) broadcastHaves(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case index, ok := <-s.haveCh:
			if !ok {
				return
			}
			// Update our own bitfield
			s.bitfield.SetPiece(index)

			peers := s.getPeers()
			for _, sp := range peers {
				if err := sp.sendHave(uint32(index)); err != nil {
					logger.Log.Debug("failed to send have", "peer", sp.peer.Conn.RemoteAddr(), "error", err)
				}
			}
		}
	}
}

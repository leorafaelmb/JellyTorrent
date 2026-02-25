package downloader

import (
	"fmt"
	"sync"

	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
)

type PieceState int

const (
	PiecePending    PieceState = iota
	PieceInProgress
	PieceCompleted
)

type PieceInfo struct {
	Index  int
	Hash   []byte
	Length uint32
}

type PieceManager struct {
	mu           sync.Mutex
	pieces       []PieceInfo
	states       []PieceState
	data         [][]byte
	owners       []string // peer addr that owns each InProgress piece
	availability []int    // per-piece count of peers that have it
	selector     PieceSelector

	completed int
	total     int
	done      chan struct{}

	haveSubsMu sync.Mutex
	haveSubs   []chan int
}

func NewPieceManager(pieces []PieceInfo, selector PieceSelector) *PieceManager {
	n := len(pieces)
	return &PieceManager{
		pieces:       pieces,
		states:       make([]PieceState, n),
		data:         make([][]byte, n),
		owners:       make([]string, n),
		availability: make([]int, n),
		selector:     selector,
		total:        n,
		done:         make(chan struct{}),
	}
}

// Assign finds a Pending piece that the peer has and transitions it to InProgress.
func (pm *PieceManager) Assign(peerAddr string, bitfield peer.BitField) (*PieceInfo, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var candidates []int
	for i, state := range pm.states {
		if state == PiecePending && bitfield.HasPiece(i) {
			candidates = append(candidates, i)
		}
	}

	idx, ok := pm.selector.Select(candidates, pm.availability)
	if !ok {
		return nil, false
	}

	pm.states[idx] = PieceInProgress
	pm.owners[idx] = peerAddr
	logger.Log.Debug("piece assigned", "piece", idx, "peer", peerAddr, "candidates", len(candidates))
	return &pm.pieces[idx], true
}

// Complete marks a piece as completed and stores its data.
func (pm *PieceManager) Complete(index int, data []byte) {
	pm.mu.Lock()

	if pm.states[index] == PieceCompleted {
		pm.mu.Unlock()
		return
	}

	pm.states[index] = PieceCompleted
	pm.data[index] = data
	pm.owners[index] = ""
	pm.completed++

	logger.Log.Debug("piece completed", "piece", index, "progress", fmt.Sprintf("%d/%d", pm.completed, pm.total))

	if pm.completed == pm.total {
		logger.Log.Info("all pieces downloaded", "total", pm.total)
		close(pm.done)
	}
	pm.mu.Unlock()

	// Notify Have subscribers (outside main lock)
	pm.haveSubsMu.Lock()
	for _, ch := range pm.haveSubs {
		select {
		case ch <- index:
		default:
		}
	}
	pm.haveSubsMu.Unlock()
}

// Release returns an InProgress piece back to Pending.
func (pm *PieceManager) Release(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.states[index] == PieceInProgress {
		pm.states[index] = PiecePending
		pm.owners[index] = ""
		logger.Log.Debug("piece released", "piece", index)
	}
}

// ReleaseAll returns all pieces owned by a peer back to Pending.
func (pm *PieceManager) ReleaseAll(peerAddr string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	released := 0
	for i, owner := range pm.owners {
		if owner == peerAddr && pm.states[i] == PieceInProgress {
			pm.states[i] = PiecePending
			pm.owners[i] = ""
			released++
		}
	}
	if released > 0 {
		logger.Log.Debug("released pieces for peer", "peer", peerAddr, "count", released)
	}
}

// IsFinished returns true if all pieces are completed.
func (pm *PieceManager) IsFinished() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.completed == pm.total
}

// Done returns a channel that is closed when all pieces are completed.
func (pm *PieceManager) Done() <-chan struct{} {
	return pm.done
}

// Results returns the ordered piece data.
func (pm *PieceManager) Results() [][]byte {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.data
}

// HasPendingFor returns true if there are Pending pieces that the peer has.
func (pm *PieceManager) HasPendingFor(bitfield peer.BitField) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i, state := range pm.states {
		if state == PiecePending && bitfield.HasPiece(i) {
			return true
		}
	}
	return false
}

// AddAvailability increments availability counts for all pieces in the bitfield.
func (pm *PieceManager) AddAvailability(bitfield peer.BitField) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i := range pm.availability {
		if bitfield.HasPiece(i) {
			pm.availability[i]++
		}
	}
}

// RemoveAvailability decrements availability counts for all pieces in the bitfield.
func (pm *PieceManager) RemoveAvailability(bitfield peer.BitField) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i := range pm.availability {
		if bitfield.HasPiece(i) {
			pm.availability[i]--
		}
	}
}

// IncrementAvailability increments the availability count for a single piece.
func (pm *PieceManager) IncrementAvailability(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if index >= 0 && index < len(pm.availability) {
		pm.availability[index]++
	}
}

// GetPieceData returns the data for a completed piece, or nil, false if not available.
func (pm *PieceManager) GetPieceData(index int) ([]byte, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if index < 0 || index >= len(pm.states) {
		return nil, false
	}
	if pm.states[index] != PieceCompleted {
		return nil, false
	}
	return pm.data[index], true
}

// SubscribeHave returns a channel that receives piece indices as they complete.
func (pm *PieceManager) SubscribeHave() chan int {
	pm.haveSubsMu.Lock()
	defer pm.haveSubsMu.Unlock()

	ch := make(chan int, 64)
	pm.haveSubs = append(pm.haveSubs, ch)
	return ch
}

// UnsubscribeHave removes a Have subscription channel.
func (pm *PieceManager) UnsubscribeHave(ch chan int) {
	pm.haveSubsMu.Lock()
	defer pm.haveSubsMu.Unlock()

	for i, sub := range pm.haveSubs {
		if sub == ch {
			pm.haveSubs = append(pm.haveSubs[:i], pm.haveSubs[i+1:]...)
			return
		}
	}
}

package downloader

import (
	"sync"

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
	return &pm.pieces[idx], true
}

// Complete marks a piece as completed and stores its data.
func (pm *PieceManager) Complete(index int, data []byte) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.states[index] == PieceCompleted {
		return
	}

	pm.states[index] = PieceCompleted
	pm.data[index] = data
	pm.owners[index] = ""
	pm.completed++

	if pm.completed == pm.total {
		close(pm.done)
	}
}

// Release returns an InProgress piece back to Pending.
func (pm *PieceManager) Release(index int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.states[index] == PieceInProgress {
		pm.states[index] = PiecePending
		pm.owners[index] = ""
	}
}

// ReleaseAll returns all pieces owned by a peer back to Pending.
func (pm *PieceManager) ReleaseAll(peerAddr string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i, owner := range pm.owners {
		if owner == peerAddr && pm.states[i] == PieceInProgress {
			pm.states[i] = PiecePending
			pm.owners[i] = ""
		}
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

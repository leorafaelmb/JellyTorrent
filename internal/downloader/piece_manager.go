package downloader

import (
	"fmt"
	"sync"

	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
	"github.com/leorafaelmb/JellyTorrent/internal/storage"
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
	store        *storage.DiskStore

	completed          int
	total              int
	done               chan struct{}
	endgameThreshold   int
	endgameActivated   bool

	// Per-piece cancel channels for endgame mode. When a piece completes,
	// its cancel channel is closed, signaling other workers downloading
	// the same piece to send Cancel messages and abort.
	cancelChs map[int]chan struct{}

	haveSubsMu sync.Mutex
	haveSubs   []chan int
}

func NewPieceManager(pieces []PieceInfo, selector PieceSelector, endgameThreshold int, store *storage.DiskStore) *PieceManager {
	n := len(pieces)
	pm := &PieceManager{
		pieces:           pieces,
		states:           make([]PieceState, n),
		data:             make([][]byte, n),
		owners:           make([]string, n),
		availability:     make([]int, n),
		selector:         selector,
		store:            store,
		total:            n,
		done:             make(chan struct{}),
		endgameThreshold: endgameThreshold,
		cancelChs:        make(map[int]chan struct{}),
	}

	// Resume: mark pieces already on disk as completed
	if store != nil {
		bf := store.CompletedBitfield()
		for i, has := range bf {
			if has {
				pm.states[i] = PieceCompleted
				pm.completed++
			}
		}
		if pm.completed > 0 {
			logger.Log.Info("resuming download", "completed", pm.completed, "total", pm.total)
		}
		if pm.completed == pm.total {
			close(pm.done)
		}
	}

	return pm
}

// Assign finds a Pending piece that the peer has and transitions it to InProgress.
// In endgame mode (few pieces remaining), also returns InProgress pieces owned by
// other peers, allowing multiple workers to download the same piece simultaneously.
func (pm *PieceManager) Assign(peerAddr string, bitfield peer.BitField) (*PieceInfo, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	remaining := pm.total - pm.completed
	endgame := pm.endgameThreshold > 0 && remaining <= pm.endgameThreshold

	if endgame && !pm.endgameActivated {
		pm.endgameActivated = true
		logger.Log.Info("endgame mode activated", "remaining", remaining)
	}

	var candidates []int
	var endgameCandidates []int
	for i, state := range pm.states {
		if !bitfield.HasPiece(i) {
			continue
		}
		if state == PiecePending {
			candidates = append(candidates, i)
		} else if endgame && state == PieceInProgress && pm.owners[i] != peerAddr {
			endgameCandidates = append(endgameCandidates, i)
		}
	}

	// Prefer pending pieces; fall back to endgame duplicates
	idx, ok := pm.selector.Select(candidates, pm.availability)
	if !ok {
		idx, ok = pm.selector.Select(endgameCandidates, pm.availability)
		if !ok {
			return nil, false
		}
	}

	// Only update state and owner for newly assigned (Pending) pieces
	if pm.states[idx] == PiecePending {
		pm.states[idx] = PieceInProgress
		pm.owners[idx] = peerAddr
	}

	logger.Log.Debug("piece assigned", "piece", idx, "peer", peerAddr, "candidates", len(candidates), "endgame", endgame)
	return &pm.pieces[idx], true
}

// CancelCh returns a channel that will be closed when the given piece is
// completed by another worker. Used in endgame mode so workers can send
// Cancel messages for in-flight block requests.
func (pm *PieceManager) CancelCh(index int) <-chan struct{} {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	ch, ok := pm.cancelChs[index]
	if !ok {
		ch = make(chan struct{})
		pm.cancelChs[index] = ch
	}
	return ch
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

	// Persist to disk and free memory
	if pm.store != nil {
		if err := pm.store.WritePiece(index, data); err != nil {
			logger.Log.Error("failed to write piece to disk", "piece", index, "error", err)
		} else {
			pm.data[index] = nil // data lives on disk now
		}
	}

	// Close the cancel channel to notify endgame workers downloading this piece
	if ch, ok := pm.cancelChs[index]; ok {
		close(ch)
		delete(pm.cancelChs, index)
	}

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

// IsEndgame returns true if endgame mode has been activated.
func (pm *PieceManager) IsEndgame() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.endgameActivated
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

// Results returns the ordered piece data, reading from disk as needed.
func (pm *PieceManager) Results() [][]byte {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.store == nil {
		return pm.data
	}

	result := make([][]byte, len(pm.data))
	for i := range pm.data {
		if pm.data[i] != nil {
			result[i] = pm.data[i]
		} else if pm.states[i] == PieceCompleted {
			data, err := pm.store.ReadPiece(i)
			if err != nil {
				logger.Log.Error("failed to read piece from disk", "piece", i, "error", err)
				continue
			}
			result[i] = data
		}
	}
	return result
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

// Progress returns the number of completed and total pieces.
func (pm *PieceManager) Progress() (completed int, total int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.completed, pm.total
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
	// Data may be nil if it was freed after writing to disk
	if pm.data[index] == nil && pm.store != nil {
		data, err := pm.store.ReadPiece(index)
		if err != nil {
			logger.Log.Error("failed to read piece from disk", "piece", index, "error", err)
			return nil, false
		}
		return data, true
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

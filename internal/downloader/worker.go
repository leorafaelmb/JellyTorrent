package downloader

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/leorafaelmb/BitTorrent-Client/internal"
	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/metainfo"
	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
)

// Worker handles downloading pieces from a single peer
type Worker struct {
	peer    *peer.Peer
	torrent *metainfo.TorrentFile
	config  Config

	attempted  int
	downloaded int
	failed     int
}

// NewWorker creates a new worker for a peer
func NewWorker(p *peer.Peer, t *metainfo.TorrentFile, cfg Config) *Worker {
	return &Worker{
		peer:    p,
		torrent: t,
		config:  cfg,
	}
}

// Run executes the worker's download loop
func (w *Worker) Run(ctx context.Context, pm *PieceManager, results chan<- *PieceResult) error {
	logger.Log.Debug("starting worker", "peer", w.peer.AddrPort)

	if err := w.connect(ctx); err != nil {
		return err
	}
	defer w.cleanup(pm)

	if err := w.setup(); err != nil {
		return err
	}

	pm.AddAvailability(w.peer.BitField)

	return w.pieceLoop(ctx, pm, results)
}

// connect establishes connection to the peer
func (w *Worker) connect(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := w.peer.Connect(); err != nil {
		return &WorkerError{
			PeerAddr: w.peer.AddrPort.String(),
			Phase:    "connection",
			Err:      err,
		}
	}

	return nil
}

// setup performs handshake and initial protocol exchange
func (w *Worker) setup() error {
	_, err := w.peer.Handshake(w.torrent.Info.InfoHash, false)
	if err != nil {
		return &WorkerError{
			PeerAddr: w.peer.AddrPort.String(),
			Phase:    "handshake",
			Err:      err,
		}
	}

	_, err = w.peer.ReadBitfield()
	if err != nil {
		return &WorkerError{
			PeerAddr: w.peer.AddrPort.String(),
			Phase:    "bitfield",
			Err:      err,
		}
	}

	if err = w.peer.SendInterested(); err != nil {
		return &WorkerError{
			PeerAddr: w.peer.AddrPort.String(),
			Phase:    "interested",
			Err:      err,
		}
	}

	if err = w.peer.WaitForUnchoke(); err != nil {
		return &WorkerError{
			PeerAddr: w.peer.AddrPort.String(),
			Phase:    "unchoke",
			Err:      err,
		}
	}

	logger.Log.Debug("worker setup complete", "peer", w.peer.AddrPort)

	return nil
}

// pieceLoop requests and downloads pieces from the peer
func (w *Worker) pieceLoop(ctx context.Context, pm *PieceManager, results chan<- *PieceResult) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pm.Done():
			return nil
		default:
		}

		info, ok := pm.Assign(w.peer.AddrPort.String(), w.peer.BitField)
		if !ok {
			if pm.IsFinished() {
				return nil
			}
			if err := w.waitForNewPieces(ctx, pm); err != nil {
				return err
			}
			continue
		}

		w.attempted++
		logger.Log.Debug("piece assigned", "peer", w.peer.AddrPort, "piece", info.Index)

		piece, err := w.peer.GetPiece(info.Hash, info.Length, uint32(info.Index))
		if err != nil {
			pm.Release(info.Index)

			if errors.Is(err, peer.ErrChoked) {
				logger.Log.Debug("choked during download, waiting", "peer", w.peer.AddrPort, "piece", info.Index)
				if err := w.peer.WaitForUnchoke(); err != nil {
					return &WorkerError{
						PeerAddr: w.peer.AddrPort.String(),
						Phase:    "unchoke-recovery",
						Err:      err,
					}
				}
				continue
			}

			w.failed++
			logger.Log.Debug("piece download failed", "peer", w.peer.AddrPort, "piece", info.Index, "error", err)
			continue
		}

		pm.Complete(info.Index, piece)
		logger.Log.Debug("piece completed", "peer", w.peer.AddrPort, "piece", info.Index)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case results <- &PieceResult{Index: info.Index, Payload: piece}:
			w.downloaded++
		}
	}
}

// waitForNewPieces sends NotInterested and waits for Have messages
// that reveal new pieces this peer can serve.
func (w *Worker) waitForNewPieces(ctx context.Context, pm *PieceManager) error {
	logger.Log.Debug("waiting for new pieces", "peer", w.peer.AddrPort)
	_ = w.peer.SendNotInterested()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pm.Done():
			return nil
		default:
		}

		w.peer.Conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		msg, err := w.peer.ReadMessage()
		w.peer.Conn.SetReadDeadline(time.Time{})

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return &WorkerError{
				PeerAddr: w.peer.AddrPort.String(),
				Phase:    "wait-for-pieces",
				Err:      err,
			}
		}

		if msg.IsKeepAlive() {
			continue
		}

		switch msg.ID {
		case internal.MessageHave:
			if len(msg.Payload) >= 4 {
				idx := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
				w.peer.BitField.SetPiece(idx)
				pm.IncrementAvailability(idx)

				if pm.HasPendingFor(w.peer.BitField) {
					logger.Log.Debug("new piece available, re-interested", "peer", w.peer.AddrPort)
					if err := w.peer.SendInterested(); err != nil {
						return &WorkerError{
							PeerAddr: w.peer.AddrPort.String(),
							Phase:    "re-interested",
							Err:      err,
						}
					}
					if err := w.peer.WaitForUnchoke(); err != nil {
						return &WorkerError{
							PeerAddr: w.peer.AddrPort.String(),
							Phase:    "re-unchoke",
							Err:      err,
						}
					}
					return nil // back to pieceLoop
				}
			}
		case internal.MessageChoke:
			w.peer.Choked = true
		case internal.MessageUnchoke:
			w.peer.Choked = false
		}
	}
}

// cleanup releases resources when the worker exits
func (w *Worker) cleanup(pm *PieceManager) {
	logger.Log.Info("worker finished",
		"peer", w.peer.AddrPort,
		"attempted", w.attempted,
		"downloaded", w.downloaded,
		"failed", w.failed,
	)

	pm.ReleaseAll(w.peer.AddrPort.String())
	pm.RemoveAvailability(w.peer.BitField)

	if w.peer.Conn != nil {
		w.peer.Conn.Close()
	}
}

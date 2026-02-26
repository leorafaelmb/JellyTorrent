package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/metainfo"
	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
)

type Downloader struct {
	ID      [20]byte
	torrent *metainfo.TorrentFile
	peers   []peer.Peer
	config  Config

	pieceManager *PieceManager
	results      chan *PieceResult

	ctx        context.Context
	cancelFunc context.CancelFunc
}

func New(t *metainfo.TorrentFile, peers []peer.Peer, opts ...Option) *Downloader {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)

	return &Downloader{
		torrent:    t,
		peers:      peers,
		config:     cfg,
		ctx:        ctx,
		cancelFunc: cancel,
	}
}

type PieceWork struct {
	Index  int
	Hash   []byte
	Length uint32
}

type PieceResult struct {
	Index   int
	Payload []byte
}

// Download orchestrates concurrent download from multiple peers using a worker pool
func (d *Downloader) Download() ([]byte, error) {
	defer d.cancelFunc()

	pieceHashes := d.torrent.Info.PieceHashes()
	numPieces := len(pieceHashes)
	pieceLength := uint32(d.torrent.Info.PieceLength)

	pieces := make([]PieceInfo, numPieces)
	for i := 0; i < numPieces; i++ {
		length := pieceLength
		if i == numPieces-1 {
			length = uint32(d.torrent.Info.Length) - pieceLength*uint32(numPieces-1)
		}
		pieces[i] = PieceInfo{Index: i, Hash: pieceHashes[i], Length: length}
	}

	selector := d.config.PieceSelector
	if selector == nil {
		selector = &RarestFirstSelector{}
	}

	var wg sync.WaitGroup
	numWorkers := min(d.config.MaxWorkers, len(d.peers))

	d.pieceManager = NewPieceManager(pieces, selector, numWorkers)
	d.results = make(chan *PieceResult, numPieces)

	logger.Log.Info("starting download", "pieces", numPieces, "workers", numWorkers)
	logger.Log.Debug("piece manager initialized", "pieces", numPieces, "pieceLength", pieceLength)

	// Start tracker updater if a tracker was configured
	if d.config.Tracker != nil {
		interval := d.config.AnnounceInterval
		if interval <= 0 {
			interval = 30 * time.Minute
		}
		tu := NewTrackerUpdater(d.config.Tracker, d.config.AnnounceReq, d.pieceManager, d.torrent.Info.Length, int(pieceLength))
		go tu.Run(d.ctx, interval)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(p peer.Peer) {
			defer wg.Done()
			worker := NewWorker(&p, d.torrent, d.config)
			if err := worker.Run(d.ctx, d.pieceManager, d.results); err != nil {
				logger.Log.Debug("worker error", "peer", p.AddrPort, "error", err)
			}
		}(d.peers[i])
	}

	go func() {
		wg.Wait()
		close(d.results)
	}()

	return d.collectResults()
}

// collectResults gathers downloaded pieces
func (d *Downloader) collectResults() ([]byte, error) {
	for {
		select {
		case <-d.ctx.Done():
			logger.Log.Error("download timed out")
			return nil, fmt.Errorf("download timeout")

		case _, ok := <-d.results:
			if !ok {
				// All workers done
				data := d.pieceManager.Results()
				if err := d.validatePieces(data); err != nil {
					return nil, err
				}
				logger.Log.Info("download complete")
				fileBytes := make([]byte, 0, d.torrent.Info.Length)
				for _, piece := range data {
					fileBytes = append(fileBytes, piece...)
				}
				return fileBytes, nil
			}
		}
	}
}

// validatePieces checks that all pieces were downloaded
func (d *Downloader) validatePieces(pieces [][]byte) error {
	var missing []int

	for i, piece := range pieces {
		if piece == nil {
			missing = append(missing, i)
		}
	}

	if len(missing) > 0 {
		return &DownloadError{
			TorrentName:  d.torrent.Info.Name,
			FailedPieces: missing,
			TotalPieces:  len(pieces),
		}
	}

	return nil
}

// SaveFile saves downloaded data to appropriate file(s)
func (d *Downloader) SaveFile(downloadPath string, data []byte) error {
	files := d.torrent.Info.GetFiles()

	if d.torrent.Info.IsSingleFile() {
		logger.Log.Info("saving file", "path", downloadPath)
		return os.WriteFile(downloadPath, data, 0644)
	}
	baseDir := filepath.Join(filepath.Dir(downloadPath), d.torrent.Info.Name)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("error creating base directory: %w", err)
	}

	offset := 0
	for _, fileInfo := range files {
		pathComponents := append([]string{baseDir}, fileInfo.Path...)
		filePath := filepath.Join(pathComponents...)

		parentDir := filepath.Dir(filePath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("error creating directory %s: %w", parentDir, err)
		}

		fileData := data[offset : offset+fileInfo.Length]

		if err := os.WriteFile(filePath, fileData, 0644); err != nil {
			return fmt.Errorf("error writing file %s: %w", filePath, err)
		}

		logger.Log.Info("wrote file", "path", filePath, "bytes", fileInfo.Length)
		offset += fileInfo.Length
	}

	return nil
}

func DownloadFile(t *metainfo.TorrentFile, peers []peer.Peer, maxWorkers int, downloadPath string, opts ...Option) error {
	allOpts := append([]Option{WithMaxWorkers(maxWorkers)}, opts...)
	d := New(t, peers, allOpts...)
	fileBytes, err := d.Download()
	if err != nil {
		return err
	}

	if err = d.SaveFile(downloadPath, fileBytes); err != nil {
		return err
	}
	return nil
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht"
	"github.com/leorafaelmb/JellyTorrent/internal/downloader"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/metainfo"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
	"github.com/leorafaelmb/JellyTorrent/internal/seeder"
	"github.com/leorafaelmb/JellyTorrent/internal/storage"
	"github.com/leorafaelmb/JellyTorrent/internal/tracker"
)

func runCommand(command string, args []string) error {
	switch command {
	case "download":
		return handleDownload(args)
	case "magnet_download":
		return handleMagnetDownload(args)
	case "seed":
		return handleSeed(args)
	default:

	}
	return nil

}

var dhtStatePath = filepath.Join(os.TempDir(), "jellytorrent", "dht_state.dat")

// defaultStorageDir returns ~/.jellytorrent/downloads/ as the base storage directory.
func defaultStorageDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "jellytorrent", "downloads")
	}
	return filepath.Join(home, ".jellytorrent", "downloads")
}

// startDHT creates and bootstraps a DHT node. Returns the DHT instance
// (caller must Close it) or nil if bootstrap fails.
func startDHT() *dht.DHT {
	os.MkdirAll(filepath.Dir(dhtStatePath), 0755)

	d, err := dht.New(
		dht.WithRoutingTable(dhtStatePath),
	)
	if err != nil {
		logger.Log.Debug("failed to create DHT node", "error", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.Bootstrap(ctx); err != nil {
		logger.Log.Debug("DHT bootstrap failed", "error", err)
		d.Close()
		return nil
	}

	logger.Log.Info("DHT bootstrapped")
	return d
}

// dhtPeers queries the DHT for peers and returns them merged with existing peers.
func dhtPeers(d *dht.DHT, infoHash [20]byte, existing []peer.Peer) []peer.Peer {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	addrs, err := d.GetPeers(ctx, infoHash)
	if err != nil {
		logger.Log.Debug("DHT get_peers failed", "error", err)
		return existing
	}

	// Deduplicate against existing peers
	seen := make(map[string]bool, len(existing))
	for _, p := range existing {
		seen[p.AddrPort.String()] = true
	}

	added := 0
	for _, addr := range addrs {
		key := addr.String()
		if !seen[key] {
			existing = append(existing, peer.Peer{AddrPort: addr})
			seen[key] = true
			added++
		}
	}

	if added > 0 {
		logger.Log.Info("DHT peers discovered", "new", added, "total", len(existing))
	}

	return existing
}

// dhtAnnounce announces to the DHT that we are downloading/seeding a torrent.
func dhtAnnounce(d *dht.DHT, infoHash [20]byte, port int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.Announce(ctx, infoHash, port); err != nil {
		logger.Log.Debug("DHT announce failed", "error", err)
	} else {
		logger.Log.Debug("DHT announce complete")
	}
}

func handleDownload(args []string) error {
	downloadFilePath := args[2]
	torrentFilePath := args[3]
	logger.Log.Debug("download destination", "path", downloadFilePath)

	t, err := metainfo.DeserializeTorrent(torrentFilePath)
	if err != nil {
		return err
	}

	logger.Log.Info("starting download",
		"torrent", torrentFilePath,
		"name", t.Info.Name,
		"pieces", len(t.Info.PieceHashes()),
		"size", t.Info.Length,
	)

	tr, err := tracker.NewMultiTracker(t.TrackerURLs())
	if err != nil {
		return err
	}
	peerID := [20]byte{}
	if _, err = rand.Read(peerID[:]); err != nil {
		return err
	}
	announceReq := tracker.AnnounceRequest{
		InfoHash:   t.Info.InfoHash,
		PeerID:     peerID,
		IP:         0,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       uint64(t.Info.Length),
		Event:      tracker.EventStarted,
	}

	trackerResp, err := tr.Announce(announceReq)
	if err != nil {
		return err
	}

	logger.Log.Info("tracker peers discovered", "count", len(trackerResp.Peers))

	// Create Peer objects from tracker addresses
	peerList := make([]peer.Peer, len(trackerResp.Peers))
	for i, addr := range trackerResp.Peers {
		addrCopy := addr
		peerList[i] = peer.Peer{AddrPort: addrCopy}
	}

	// DHT: bootstrap, discover additional peers, announce
	d := startDHT()
	if d != nil {
		defer func() {
			d.Save(dhtStatePath)
			d.Close()
		}()
		peerList = dhtPeers(d, t.Info.InfoHash, peerList)
		go dhtAnnounce(d, t.Info.InfoHash, 6881)
	}

	storageDir := defaultStorageDir()
	logger.Log.Debug("storage directory", "path", filepath.Join(storageDir, hex.EncodeToString(t.Info.InfoHash[:])))

	if err = downloader.DownloadFile(t, peerList, 50, downloadFilePath,
		downloader.WithTracker(tr, announceReq),
		downloader.WithAnnounceInterval(trackerResp.Interval),
		downloader.WithStorageDir(storageDir),
	); err != nil {
		return err
	}

	// Best-effort stopped announce
	announceReq.Event = tracker.EventStopped
	announceReq.Left = 0
	tr.Announce(announceReq)

	if t.Info.IsSingleFile() {
		logger.Log.Info("download complete", "path", downloadFilePath)
	} else {
		logger.Log.Info("download complete", "dir", filepath.Join(filepath.Dir(downloadFilePath), t.Info.Name))
	}

	return nil
}

func handleMagnetDownload(args []string) error {
	downloadFilePath := args[2]
	magnetURL := args[3]

	logger.Log.Info("starting magnet download")

	p, magnet, err := downloader.ConnectToMagnetPeer(magnetURL)
	defer p.Conn.Close()

	metadata, err := p.DownloadMetadata(magnet)
	if err != nil {
		return err
	}

	t := metainfo.TorrentFile{
		Announce: magnet.TrackerURL(),
		Info:     metadata,
	}
	t.Info.InfoHash = magnet.InfoHash

	logger.Log.Info("metadata downloaded", "name", t.Info.Name)

	tr, err := tracker.NewMultiTracker(magnet.TrackerURLs)
	if err != nil {
		return err
	}

	annReq := tracker.AnnounceRequest{
		InfoHash:   magnet.InfoHash,
		PeerID:     [20]byte{},
		IP:         0,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       uint64(t.Info.Length),
		Event:      tracker.EventStarted,
	}

	annResp, err := tr.Announce(annReq)
	if err != nil {
		return err
	}

	logger.Log.Info("tracker peers discovered", "count", len(annResp.Peers))

	peerList := make([]peer.Peer, len(annResp.Peers))
	for i, addr := range annResp.Peers {
		peerList[i] = peer.Peer{AddrPort: addr}
	}

	// DHT: bootstrap, discover additional peers, announce
	d := startDHT()
	if d != nil {
		defer func() {
			d.Save(dhtStatePath)
			d.Close()
		}()
		peerList = dhtPeers(d, magnet.InfoHash, peerList)
		go dhtAnnounce(d, magnet.InfoHash, 6881)
	}

	storageDir := defaultStorageDir()
	logger.Log.Debug("storage directory", "path", filepath.Join(storageDir, hex.EncodeToString(magnet.InfoHash[:])))

	if err = downloader.DownloadFile(&t, peerList, 50, downloadFilePath,
		downloader.WithTracker(tr, annReq),
		downloader.WithAnnounceInterval(annResp.Interval),
		downloader.WithStorageDir(storageDir),
	); err != nil {
		return err
	}

	// Best-effort stopped announce
	annReq.Event = tracker.EventStopped
	annReq.Left = 0
	tr.Announce(annReq)

	if t.Info.IsSingleFile() {
		logger.Log.Info("download complete", "path", downloadFilePath)
	} else {
		logger.Log.Info("download complete", "dir", filepath.Join(filepath.Dir(downloadFilePath), t.Info.Name))
	}

	return nil
}

func handleSeed(args []string) error {
	// Usage: seed -o <storage-dir> <torrent-file>
	if len(args) < 4 {
		return fmt.Errorf("usage: seed -o <storage-dir> <torrent-file>")
	}

	storageDir := args[2]
	torrentFilePath := args[3]

	t, err := metainfo.DeserializeTorrent(torrentFilePath)
	if err != nil {
		return err
	}

	pieceHashes := t.Info.PieceHashes()
	numPieces := len(pieceHashes)
	pieceLength := t.Info.PieceLength

	logger.Log.Info("seeding",
		"torrent", torrentFilePath,
		"name", t.Info.Name,
		"pieces", numPieces,
	)

	// Open existing DiskStore
	store, err := storage.NewDiskStore(storageDir, t.Info.InfoHash, numPieces, pieceLength, t.Info.Length)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}

	// Verify all pieces are present
	bf := store.CompletedBitfield()
	missing := 0
	for _, has := range bf {
		if !has {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Errorf("cannot seed: %d of %d pieces missing from storage", missing, numPieces)
	}

	// Build PieceManager with all pieces completed (for BlockServer)
	pieces := make([]downloader.PieceInfo, numPieces)
	for i := 0; i < numPieces; i++ {
		length := uint32(pieceLength)
		if i == numPieces-1 {
			length = uint32(t.Info.Length) - uint32(pieceLength)*uint32(numPieces-1)
		}
		pieces[i] = downloader.PieceInfo{Index: i, Hash: pieceHashes[i], Length: length}
	}
	pm := downloader.NewPieceManager(pieces, &downloader.RarestFirstSelector{}, 0, store)
	blockServer := downloader.NewBlockServer(pm)

	// Create seeder
	completedBitfield := pm.CompletedBitfield()
	s := seeder.New(t.Info.InfoHash, numPieces, blockServer, completedBitfield, nil)

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start seeder in a goroutine so we can do DHT/tracker work
	seederErr := make(chan error, 1)
	go func() {
		seederErr <- s.Run(ctx)
	}()

	// Wait briefly for listener to bind
	time.Sleep(100 * time.Millisecond)

	// DHT: bootstrap and announce with actual listen port
	d := startDHT()
	if d != nil {
		defer func() {
			d.Save(dhtStatePath)
			d.Close()
		}()
		go dhtAnnounce(d, t.Info.InfoHash, s.ListenPort())

		// Periodic DHT re-announce
		go func() {
			ticker := time.NewTicker(15 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					dhtAnnounce(d, t.Info.InfoHash, s.ListenPort())
				}
			}
		}()
	}

	// Tracker announce with Left=0
	seedTr, trErr := tracker.NewMultiTracker(t.TrackerURLs())
	if trErr == nil {
		annReq := tracker.AnnounceRequest{
			InfoHash: t.Info.InfoHash,
			Port:     uint16(s.ListenPort()),
			Left:     0,
			Event:    tracker.EventCompleted,
		}
		if _, err := seedTr.Announce(annReq); err != nil {
			logger.Log.Debug("tracker announce failed", "error", err)
		}

		defer func() {
			annReq.Event = tracker.EventStopped
			seedTr.Announce(annReq)
		}()
	}

	logger.Log.Info("seeding started, press Ctrl+C to stop", "port", s.ListenPort())

	return <-seederErr
}


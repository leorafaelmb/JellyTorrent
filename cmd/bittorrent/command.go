package main

import (
	"crypto/rand"
	"path/filepath"

	"github.com/leorafaelmb/BitTorrent-Client/internal/downloader"
	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/metainfo"
	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
	"github.com/leorafaelmb/BitTorrent-Client/internal/tracker"
)

func runCommand(command string, args []string) error {
	switch command {
	case "download":
		return handleDownload(args)
	case "magnet_download":
		return handleMagnetDownload(args)
	default:

	}
	return nil

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

	logger.Log.Info("peers discovered", "count", len(trackerResp.Peers))

	// Create Peer objects from addresses
	peerList := make([]peer.Peer, len(trackerResp.Peers))
	for i, addr := range trackerResp.Peers {
		addrCopy := addr
		peerList[i] = peer.Peer{AddrPort: addrCopy}
	}

	if err = downloader.DownloadFile(t, peerList, 50, downloadFilePath,
		downloader.WithTracker(tr, announceReq),
		downloader.WithAnnounceInterval(trackerResp.Interval),
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

	p, magnet, err := ConnectToMagnetPeer(magnetURL)
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

	logger.Log.Info("peers discovered", "count", len(annResp.Peers))

	peerList := make([]peer.Peer, len(annResp.Peers))
	for i, addr := range annResp.Peers {
		peerList[i] = peer.Peer{AddrPort: addr}
	}

	if err = downloader.DownloadFile(&t, peerList, 50, downloadFilePath,
		downloader.WithTracker(tr, annReq),
		downloader.WithAnnounceInterval(annResp.Interval),
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

func ConnectToMagnetPeer(magnetURL string) (*peer.Peer, *metainfo.MagnetLink, error) {
	magnet, err := metainfo.DeserializeMagnet(magnetURL)
	if err != nil {
		return nil, nil, err
	}
	tr, err := tracker.NewMultiTracker(magnet.TrackerURLs)
	if err != nil {
		return nil, nil, err
	}

	trReq := tracker.AnnounceRequest{
		InfoHash:   magnet.InfoHash,
		PeerID:     [20]byte{},
		Uploaded:   0,
		Downloaded: 0,
		Left:       0,
		Event:      0,
	}

	ann, err := tr.Announce(trReq)
	if err != nil {
		return nil, nil, err
	}

	logger.Log.Debug("connecting to magnet peer", "addr", ann.Peers[0])

	p := &peer.Peer{AddrPort: ann.Peers[0]}
	if err = p.Connect(); err != nil {
		return nil, nil, err
	}

	if _, err = p.MagnetHandshake(magnet.InfoHash); err != nil {
		p.Conn.Close()
		return nil, nil, err
	}

	if _, err = p.ReadBitfield(); err != nil {
		p.Conn.Close()
		return nil, nil, err
	}

	logger.Log.Debug("magnet peer ready", "addr", ann.Peers[0])

	return p, magnet, nil
}

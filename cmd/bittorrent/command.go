package main

import (
	"crypto/rand"
	"fmt"
	"github.com/leorafaelmb/BitTorrent-Client/internal/downloader"
	"github.com/leorafaelmb/BitTorrent-Client/internal/metainfo"
	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
	"github.com/leorafaelmb/BitTorrent-Client/internal/tracker"
	"path/filepath"
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
	downloadFilePath := args[3]
	torrentFilePath := args[4]
	fmt.Println(downloadFilePath)

	t, err := metainfo.DeserializeTorrent(torrentFilePath)
	if err != nil {
		return err
	}

	fmt.Println("\nStarting download...")

	tr, err := tracker.NewTracker(t.Announce)
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
		Left:       uint64(len(t.Info.Pieces)),
		Event:      0,
	}

	trackerResp, err := tr.Announce(announceReq)
	if err != nil {
		return err
	}

	if err != nil {
		return err
	}
	fmt.Printf("Found %d peers\n", len(trackerResp.Peers))

	// Create Peer objects from addresses
	peerList := make([]peer.Peer, len(trackerResp.Peers))
	for i, addr := range trackerResp.Peers {
		addrCopy := addr
		peerList[i] = peer.Peer{AddrPort: addrCopy}
	}

	if err = downloader.DownloadFile(t, peerList, 50, downloadFilePath); err != nil {
		return err
	}

	if t.Info.IsSingleFile() {
		fmt.Printf("File saved to: %s\n", downloadFilePath)
	} else {
		fmt.Printf("Files saved to directory: %s\n", filepath.Join(filepath.Dir(downloadFilePath), t.Info.Name))
	}

	return nil
}

func handleMagnetDownload(args []string) error {
	downloadFilePath := args[3]
	magnetURL := args[4]

	p, magnet, err := ConnectToMagnetPeer(magnetURL)
	defer p.Conn.Close()

	metadata, err := p.DownloadMetadata(magnet)
	if err != nil {
		return err
	}

	t := metainfo.TorrentFile{
		Announce: magnet.TrackerURL,
		Info:     metadata,
	}
	t.Info.InfoHash = magnet.InfoHash

	tr, err := tracker.NewTracker(magnetURL)
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
		Left:       uint64(len(t.Info.Pieces)),
		Event:      0,
	}

	annResp, err := tr.Announce(annReq)
	if err != nil {
		return err
	}

	peerList := make([]peer.Peer, len(annResp.Peers))
	for i, addr := range annResp.Peers {
		peerList[i] = peer.Peer{AddrPort: addr}
	}

	if err = downloader.DownloadFile(&t, peerList, 50, downloadFilePath); err != nil {
		return err
	}

	if t.Info.IsSingleFile() {
		fmt.Printf("File saved to: %s\n", downloadFilePath)
	} else {
		fmt.Printf("Files saved to directory: %s\n", filepath.Join(filepath.Dir(downloadFilePath), t.Info.Name))
	}

	return nil
}

func ConnectToMagnetPeer(magnetURL string) (*peer.Peer, *metainfo.MagnetLink, error) {
	magnet, err := metainfo.DeserializeMagnet(magnetURL)
	if err != nil {
		return nil, nil, err
	}
	tr, err := tracker.NewTracker(magnetURL)
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

	return p, magnet, nil
}

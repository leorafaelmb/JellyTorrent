package tracker

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type Tracker interface {
	Announce(req AnnounceRequest) (AnnounceResponse, error)
	Scrape(infoHashes [][20]byte) (ScrapeFiles, error)
}

type AnnounceRequest struct {
	InfoHash   [20]byte // 20-byte sha1 hash of the bencoded form of the info value from the metainfo file
	PeerID     [20]byte // 20 byte string which this downloader uses as its id. Each downloader generates its own id at random at the start of a new download.
	IP         uint32   // optional parameter giving the IP (or dns name) which this peer is at.
	Port       uint16   // the port number this peer is listening on. try listening on port 6881, try up to 6889.
	Uploaded   uint64   // total amount uploaded so far
	Downloaded uint64   // total amount downloaded so far
	Left       uint64   // number of bytes this peer still has to download. cannot be computed from downloaded and file length

	// optional key which maps to started, completed, stopped, or empty. if empty, this is one of
	// the announcements done at regular intervals. an announcement using started is sent when a
	// download first begins, and one using completed is sent when the download is complete. no
	// completed is sent if the file was complete when started. downloaders send an announcement
	// using stopped when they cease downloading
	Event uint32
}

type AnnounceResponse struct {
	Interval time.Duration    // number of seconds the downloader should wait between regular rerequests
	Peers    []netip.AddrPort // list of peers containing IP address and port
}

type ScrapeFiles map[[20]byte]FileStats

type FileStats struct {
	Seeders   uint32
	Completed uint32
	Leechers  uint32
}

func NewTracker(trackerURL string) (Tracker, error) {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return nil, err
	}
	proto := strings.ToLower(u.Scheme)

	switch proto {
	case "udp":
		tr, err := newUDPTracker(u.Host)
		if err != nil {
			return nil, fmt.Errorf("NewTracker: %w", err)
		}
		return tr, nil
	case "http", "https":
		return newHTTPTracker(trackerURL), nil
	default:
		return nil, ErrUnsupportedScheme
	}
}

// urlEncodeInfoHash URL-encodes a hexadecimal-represented info hash
func urlEncodeInfoHash(infoHash string) string {
	urlEncodedHash := ""
	for i := 0; i < len(infoHash); i += 2 {
		urlEncodedHash += fmt.Sprintf("%%%s%s", string(infoHash[i]), string(infoHash[i+1]))
	}
	return urlEncodedHash
}

func unmarshalPeerAddresses(numPeers int, data []byte) ([]netip.AddrPort, error) {
	peers := make([]netip.AddrPort, numPeers)

	for i := 0; i < numPeers; i++ {
		index := i * 6
		peerAddr, ok := netip.AddrFromSlice(data[index : index+4])
		if !ok {
			return nil, fmt.Errorf("slice length is not 4 or 16")
		}
		port := binary.BigEndian.Uint16(data[index+4 : index+6])

		peerAddrPort := netip.AddrPortFrom(peerAddr, port)
		peers[i] = peerAddrPort
	}
	return peers, nil
}

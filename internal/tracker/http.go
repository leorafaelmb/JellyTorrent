package tracker

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/leorafaelmb/bencode"
	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
)

type HTTPTracker struct {
	url string
}

func newHTTPTracker(url string) *HTTPTracker {
	return &HTTPTracker{url: url}
}

func (t *HTTPTracker) Announce(req AnnounceRequest) (AnnounceResponse, error) {
	logger.Log.Debug("HTTP announce request", "url", t.url)

	resp, err := http.Get(t.marshalHTTPAnnounceRequest(req))
	if err != nil {
		return AnnounceResponse{}, fmt.Errorf("error sending HTTP request to tracker: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return AnnounceResponse{}, fmt.Errorf("error reading tracker response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return AnnounceResponse{}, &TrackerError{Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))}
	}
	trackerResponse, err := unmarshalHTTPAnnounceResponse(body)
	if err != nil {
		return AnnounceResponse{}, err
	}

	logger.Log.Debug("HTTP announce response", "peers", len(trackerResponse.Peers))

	return trackerResponse, nil
}

func (t *HTTPTracker) Scrape(infoHashes [][20]byte) (ScrapeFiles, error) {
	announceLoc := strings.LastIndex(t.url, "announce")
	if announceLoc == -1 {
		return nil, &ResponseError{Context: "Scrape", Reason: "tracker URL does not contain 'announce'"}
	}
	scrapeURL := t.url[:announceLoc] + strings.Replace(t.url[announceLoc:], "announce", "scrape?", 1)
	for _, infoHash := range infoHashes {
		scrapeURL += fmt.Sprintf("info_hash=%s&", urlEncodeInfoHash(fmt.Sprintf("%x", infoHash)))
	}
	scrapeURL = scrapeURL[:len(scrapeURL)-1]

	resp, err := http.Get(scrapeURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &TrackerError{Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))}
	}

	return unmarshalHTTPScrapeResponse(body)
}

func (t *HTTPTracker) marshalHTTPAnnounceRequest(req AnnounceRequest) string {
	urlInfoHash := urlEncodeInfoHash(fmt.Sprintf("%x", req.InfoHash))
	result := fmt.Sprintf(
		"%s?info_hash=%s&peer_id=%s&port=%d&uploaded=%d&downloaded=%d&left=%d&compact=1",
		t.url, urlInfoHash, url.QueryEscape(string(req.PeerID[:])), req.Port, req.Uploaded, req.Downloaded,
		req.Left)
	if eventStr, ok := EventStrings[req.Event]; ok {
		result += "&event=" + eventStr
	}
	return result
}

func unmarshalHTTPAnnounceResponse(response []byte) (AnnounceResponse, error) {
	decoded, err := bencode.Decode(response)
	if err != nil {
		return AnnounceResponse{}, &ResponseError{Context: "unmarshalHTTPAnnounceResponse", Reason: fmt.Sprintf("decode error: %v", err)}
	}
	d, ok := decoded.(map[string]interface{})
	if !ok {
		return AnnounceResponse{}, &ResponseError{Context: "unmarshalHTTPAnnounceResponse", Reason: "decoded did not return map[string]interface{}"}
	}
	if reason, ok := d["failure reason"].(string); ok {
		return AnnounceResponse{}, &TrackerError{Message: reason}
	}
	interval, ok := d["interval"].(int)
	if !ok {
		return AnnounceResponse{}, &ResponseError{Context: "unmarshalHTTPAnnounceResponse", Reason: "missing or invalid interval"}
	}

	peers, err := unmarshalBencodedPeers(d["peers"])
	if err != nil {
		return AnnounceResponse{}, err
	}

	return AnnounceResponse{
		Interval: time.Duration(interval) * time.Second,
		Peers:    peers,
	}, nil
}

func unmarshalBencodedPeers(encPeers interface{}) ([]netip.AddrPort, error) {
	switch p := encPeers.(type) {
	case []byte:
		return unmarshalPeerAddresses(len(p)/6, p)
	case string:
		peerBytes := []byte(p)
		return unmarshalPeerAddresses(len(peerBytes)/6, peerBytes)
	case []interface{}:
		peers := make([]netip.AddrPort, len(p))
		for i, item := range p {
			peerInfo, ok := item.(map[string]interface{})
			if !ok {
				return nil, &ResponseError{Context: "unmarshalBencodedPeers", Reason: fmt.Sprintf("peer %d is not a dictionary", i)}
			}
			ip, ok := peerInfo["ip"].(string)
			if !ok {
				return nil, &ResponseError{Context: "unmarshalBencodedPeers", Reason: fmt.Sprintf("peer %d: missing or invalid ip", i)}
			}
			port, ok := peerInfo["port"].(int)
			if !ok {
				return nil, &ResponseError{Context: "unmarshalBencodedPeers", Reason: fmt.Sprintf("peer %d: missing or invalid port", i)}
			}
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				return nil, &ResponseError{Context: "unmarshalBencodedPeers", Reason: fmt.Sprintf("peer %d: error parsing ip %q: %v", i, ip, err)}
			}
			peers[i] = netip.AddrPortFrom(addr, uint16(port))
		}
		return peers, nil
	default:
		return nil, &ResponseError{Context: "unmarshalBencodedPeers", Reason: fmt.Sprintf("unexpected peers type: %T", encPeers)}
	}
}

func unmarshalHTTPScrapeResponse(data []byte) (ScrapeFiles, error) {
	decoded, err := bencode.Decode(data)
	if err != nil {
		return nil, &ResponseError{Context: "unmarshalHTTPScrapeResponse", Reason: fmt.Sprintf("decode error: %v", err)}
	}

	d, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, &ResponseError{Context: "unmarshalHTTPScrapeResponse", Reason: fmt.Sprintf("expected dict, got %T", decoded)}
	}

	files, ok := d["files"].(map[string]interface{})
	if !ok {
		return nil, &ResponseError{Context: "unmarshalHTTPScrapeResponse", Reason: "missing or invalid 'files' dict"}
	}

	resp := ScrapeFiles{}
	for k, v := range files {
		if len(k) != 20 {
			return nil, &ResponseError{Context: "unmarshalHTTPScrapeResponse", Reason: fmt.Sprintf("invalid info hash length: %d", len(k))}
		}
		infoHash := [20]byte{}
		copy(infoHash[:], k)

		stats, ok := v.(map[string]interface{})
		if !ok {
			return nil, &ResponseError{Context: "unmarshalHTTPScrapeResponse", Reason: fmt.Sprintf("expected dict for file stats, got %T", v)}
		}

		complete, _ := stats["complete"].(int)
		downloaded, _ := stats["downloaded"].(int)
		incomplete, _ := stats["incomplete"].(int)

		resp[infoHash] = FileStats{
			Seeders:   uint32(complete),
			Completed: uint32(downloaded),
			Leechers:  uint32(incomplete),
		}
	}

	return resp, nil
}

package tracker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"time"
)

type UDPTracker struct {
	url  string
	conn UDPTrackerConn
	mu   sync.Mutex
}

type UDPTrackerConn struct {
	*net.UDPConn
	connectionID uint64
	connectedAt  time.Time
	n            int
}

func (t *UDPTracker) connectionExpired() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.conn.connectedAt.IsZero() && time.Since(t.conn.connectedAt) >= 60*time.Second
}

// NewTracker creates a UDPTracker with all values filled.
// Returns a UDPTracker that has already connected to the tracker and any error that may have occurred.
func newUDPTracker(url string) (*UDPTracker, error) {
	t := &UDPTracker{}
	addr, err := net.ResolveUDPAddr("udp", url)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	t.url = url
	t.conn = UDPTrackerConn{
		UDPConn:      conn,
		connectionID: 0,
		n:            0,
	}
	if _, err = t.connect(); err != nil {
		return nil, err
	}

	return t, nil
}

// connect() connects the BitTorrent client to the UDP tracker.
// returns the connectionID and any error that may have occurred.
func (t *UDPTracker) connect() (uint64, error) {
	reqBytes, transactionID := marshalConnectRequest()

	respBytes, err := t.sendWithDeadline(reqBytes, true)
	if err != nil {
		return 0, err
	}

	connectionID, err := unmarshalConnectResponse(transactionID, respBytes)
	if err != nil {
		return 0, err
	}

	t.conn.connectionID = connectionID
	t.mu.Lock()
	t.conn.connectedAt = time.Now()
	t.mu.Unlock()

	return connectionID, nil

}

func (t *UDPTracker) Announce(req AnnounceRequest) (AnnounceResponse, error) {
	reqBytes, transactionID := t.marshalAnnounceRequest(req, 0, 0)
	respBytes, err := t.sendWithDeadline(reqBytes, false)
	if err != nil {
		return AnnounceResponse{}, err
	}
	response, err := unmarshalUDPAnnounceResponse(transactionID, respBytes)
	if err != nil {
		return response, err
	}

	return response, nil

}

func (t *UDPTracker) Scrape(infoHashes [][20]byte) (ScrapeFiles, error) {
	reqBytes, transactionID := t.marshalUDPScrapeRequest(infoHashes)
	respBytes, err := t.sendWithDeadline(reqBytes, false)
	if err != nil {
		return nil, err
	}
	resp, err := unmarshalUDPScrapeResponse(transactionID, infoHashes, respBytes)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (t *UDPTracker) sendWithDeadline(data []byte, connect bool) ([]byte, error) {
	buf := make([]byte, 4096)
	for t.conn.n <= 8 {
		if t.connectionExpired() && !connect {
			if _, err := t.connect(); err != nil {
				return nil, fmt.Errorf("sendWithDeadline: reconnect failed: %w", err)
			}
			continue
		}

		if _, err := t.conn.Write(data); err != nil {
			return nil, err
		}
		if err := t.conn.SetReadDeadline(t.getDeadline()); err != nil {
			return nil, err
		}
		n, err := t.conn.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				t.conn.n++
				continue
			}
			return nil, err
		}
		t.conn.n = 0
		resp := make([]byte, n)
		copy(resp, buf[:n])
		return resp, nil
	}
	return nil, ErrMaxRetriesExceeded
}

func (t *UDPTracker) getDeadline() time.Time {
	seconds := 15 * math.Pow(2, float64(t.conn.n))
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

//
// **SERDES**
//

func marshalConnectRequest() ([]byte, uint32) {
	result := make([]byte, 16)
	transactionID := rand.Uint32()
	binary.BigEndian.PutUint64(result[0:8], UDPProtocolID)
	binary.BigEndian.PutUint32(result[8:12], ActionConnect)
	binary.BigEndian.PutUint32(result[12:16], transactionID)
	return result, transactionID
}

func unmarshalConnectResponse(reqTransactionID uint32, resp []byte) (uint64, error) {
	if len(resp) < 16 {
		return 0, &ResponseError{Context: "unmarshalConnectResponse", Reason: fmt.Sprintf("response too short: %d bytes", len(resp))}
	}
	action := binary.BigEndian.Uint32(resp[0:4])
	if action == ActionError && len(resp) > 8 {
		return 0, &TrackerError{Message: string(resp[8:])}
	}
	if action != ActionConnect {
		return 0, &ResponseError{Context: "unmarshalConnectResponse", Reason: fmt.Sprintf("unexpected action %d, expected %d", action, ActionConnect)}
	}
	transactionID := binary.BigEndian.Uint32(resp[4:8])
	if transactionID != reqTransactionID {
		return 0, &ResponseError{Context: "unmarshalConnectResponse", Reason: fmt.Sprintf("transaction ID mismatch: got %d, expected %d", transactionID, reqTransactionID)}
	}
	connectionID := binary.BigEndian.Uint64(resp[8:16])

	return connectionID, nil
}

func (t *UDPTracker) marshalAnnounceRequest(req AnnounceRequest, key uint32, numWant uint32) ([]byte, uint32) {
	reqBytes := make([]byte, 98)
	transactionID := rand.Uint32()
	binary.BigEndian.PutUint64(reqBytes[0:8], t.conn.connectionID)
	binary.BigEndian.PutUint32(reqBytes[8:12], ActionAnnounce)
	binary.BigEndian.PutUint32(reqBytes[12:16], transactionID)
	copy(reqBytes[16:36], req.InfoHash[:])
	copy(reqBytes[36:56], req.PeerID[:])
	binary.BigEndian.PutUint64(reqBytes[56:64], req.Downloaded)
	binary.BigEndian.PutUint64(reqBytes[64:72], req.Left)
	binary.BigEndian.PutUint64(reqBytes[72:80], req.Uploaded)
	binary.BigEndian.PutUint32(reqBytes[80:84], req.Event)
	binary.BigEndian.PutUint32(reqBytes[84:88], req.IP)
	binary.BigEndian.PutUint32(reqBytes[88:92], key)
	binary.BigEndian.PutUint32(reqBytes[92:96], numWant)
	binary.BigEndian.PutUint16(reqBytes[96:98], req.Port)
	return reqBytes, transactionID
}

func unmarshalUDPAnnounceResponse(reqTransactionID uint32, data []byte) (AnnounceResponse, error) {
	resp := AnnounceResponse{}
	if len(data) < 8 {
		return resp, &ResponseError{Context: "unmarshalUDPAnnounceResponse", Reason: fmt.Sprintf("response too short: %d bytes", len(data))}
	}
	action := binary.BigEndian.Uint32(data[0:4])
	if action == ActionError && len(data) > 8 {
		return resp, &TrackerError{Message: string(data[8:])}
	}
	if action != ActionAnnounce {
		return resp, &ResponseError{Context: "unmarshalUDPAnnounceResponse", Reason: fmt.Sprintf("unexpected action %d, expected %d", action, ActionAnnounce)}
	}
	if len(data) < 20 {
		return resp, &ResponseError{Context: "unmarshalUDPAnnounceResponse", Reason: fmt.Sprintf("response too short: %d bytes", len(data))}
	}
	transactionID := binary.BigEndian.Uint32(data[4:8])
	if transactionID != reqTransactionID {
		return resp, &ResponseError{Context: "unmarshalUDPAnnounceResponse", Reason: fmt.Sprintf("transaction ID mismatch: got %d, expected %d", transactionID, reqTransactionID)}
	}

	resp.Interval = time.Duration(binary.BigEndian.Uint32(data[8:12])) * time.Second
	leechers := binary.BigEndian.Uint32(data[12:16])
	seeders := binary.BigEndian.Uint32(data[16:20])
	peers, err := unmarshalPeerAddresses(int(seeders+leechers), data[20:])
	if err != nil {
		return resp, err
	}
	resp.Peers = peers

	return resp, nil
}

func (t *UDPTracker) marshalUDPScrapeRequest(infoHashes [][20]byte) ([]byte, uint32) {
	reqBytes := make([]byte, 16+20*len(infoHashes))
	transactionID := rand.Uint32()
	binary.BigEndian.PutUint64(reqBytes[0:8], t.conn.connectionID)
	binary.BigEndian.PutUint32(reqBytes[8:12], ActionScrape)
	binary.BigEndian.PutUint32(reqBytes[12:16], transactionID)
	for i, j := 0, 16; i < len(infoHashes) && j < 16+20*len(infoHashes); i, j = i+1, j+20 {
		copy(reqBytes[j:j+20], infoHashes[i][:])
	}

	return reqBytes, transactionID
}

func unmarshalUDPScrapeResponse(reqTransactionID uint32, infoHashes [][20]byte, data []byte) (ScrapeFiles, error) {
	if len(data) < 8 {
		return nil, &ResponseError{Context: "unmarshalUDPScrapeResponse", Reason: fmt.Sprintf("response too short: %d bytes", len(data))}
	}
	action := binary.BigEndian.Uint32(data[0:4])
	if action == ActionError && len(data) > 8 {
		return nil, &TrackerError{Message: string(data[8:])}
	}
	if action != ActionScrape {
		return nil, &ResponseError{Context: "unmarshalUDPScrapeResponse", Reason: fmt.Sprintf("unexpected action %d, expected %d", action, ActionScrape)}
	}
	transactionID := binary.BigEndian.Uint32(data[4:8])
	if transactionID != reqTransactionID {
		return nil, &ResponseError{Context: "unmarshalUDPScrapeResponse", Reason: fmt.Sprintf("transaction ID mismatch: got %d, expected %d", transactionID, reqTransactionID)}
	}
	sf := ScrapeFiles{}

	for i, j := 0, 8; i < len(infoHashes) && j+12 <= len(data); i, j = i+1, j+12 {
		sf[infoHashes[i]] = FileStats{
			Seeders:   binary.BigEndian.Uint32(data[j : j+4]),
			Completed: binary.BigEndian.Uint32(data[j+4 : j+8]),
			Leechers:  binary.BigEndian.Uint32(data[j+8 : j+12]),
		}
	}

	return sf, nil

}

package tracker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// marshalConnectRequest

func TestMarshalConnectRequest(t *testing.T) {
	data, txnID := marshalConnectRequest()

	if len(data) != 16 {
		t.Fatalf("expected 16 bytes, got %d", len(data))
	}
	protocolID := binary.BigEndian.Uint64(data[0:8])
	if protocolID != UDPProtocolID {
		t.Errorf("protocol ID = 0x%x, want 0x%x", protocolID, UDPProtocolID)
	}
	action := binary.BigEndian.Uint32(data[8:12])
	if action != ActionConnect {
		t.Errorf("action = %d, want %d", action, ActionConnect)
	}
	gotTxn := binary.BigEndian.Uint32(data[12:16])
	if gotTxn != txnID {
		t.Errorf("transaction ID mismatch: data has %d, returned %d", gotTxn, txnID)
	}
}

// unmarshalConnectResponse

func TestUnmarshalConnectResponse(t *testing.T) {
	tests := []struct {
		name    string
		txnID   uint32
		data    []byte
		wantID  uint64
		wantErr string
	}{
		{
			name:   "valid",
			txnID:  42,
			data:   buildConnectResponse(ActionConnect, 42, 0xDEADBEEF),
			wantID: 0xDEADBEEF,
		},
		{
			name:    "too short",
			txnID:   42,
			data:    []byte{0, 0, 0},
			wantErr: "too short",
		},
		{
			name:    "wrong action",
			txnID:   42,
			data:    buildConnectResponse(ActionAnnounce, 42, 0),
			wantErr: "unexpected action",
		},
		{
			name:    "wrong transaction ID",
			txnID:   42,
			data:    buildConnectResponse(ActionConnect, 99, 0),
			wantErr: "transaction ID mismatch",
		},
		{
			name:    "error action with message",
			txnID:   42,
			data:    buildErrorResponse(42, "connection refused"),
			wantErr: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connID, err := unmarshalConnectResponse(tt.txnID, tt.data)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				// Verify structured error types
				switch tt.name {
				case "too short", "wrong action", "wrong transaction ID":
					var re *ResponseError
					if !errors.As(err, &re) {
						t.Errorf("expected *ResponseError, got %T", err)
					}
				case "error action with message":
					var te *TrackerError
					if !errors.As(err, &te) {
						t.Errorf("expected *TrackerError, got %T", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if connID != tt.wantID {
				t.Errorf("connectionID = 0x%x, want 0x%x", connID, tt.wantID)
			}
		})
	}
}

// marshalAnnounceRequest

func TestMarshalAnnounceRequest(t *testing.T) {
	tracker := &UDPTracker{
		conn: UDPTrackerConn{connectionID: 0xCAFEBABE},
	}
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	peerID := [20]byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	req := AnnounceRequest{
		InfoHash: infoHash,
		PeerID:   peerID,
		Port:     6881,
		Event:    EventStarted,
	}

	data, _ := tracker.marshalAnnounceRequest(req, 0, 0)

	if len(data) != 98 {
		t.Fatalf("expected 98 bytes, got %d", len(data))
	}

	// Connection ID
	connID := binary.BigEndian.Uint64(data[0:8])
	if connID != 0xCAFEBABE {
		t.Errorf("connectionID = 0x%x, want 0xCAFEBABE", connID)
	}

	// Action
	action := binary.BigEndian.Uint32(data[8:12])
	if action != ActionAnnounce {
		t.Errorf("action = %d, want %d", action, ActionAnnounce)
	}

	// Info hash
	if !bytes.Equal(data[16:36], infoHash[:]) {
		t.Error("info hash mismatch")
	}

	// Peer ID
	if !bytes.Equal(data[36:56], peerID[:]) {
		t.Error("peer ID mismatch")
	}

	// Port
	port := binary.BigEndian.Uint16(data[96:98])
	if port != 6881 {
		t.Errorf("port = %d, want 6881", port)
	}
}

// unmarshalUDPAnnounceResponse

func TestUnmarshalUDPAnnounceResponse(t *testing.T) {
	tests := []struct {
		name      string
		txnID     uint32
		data      []byte
		wantPeers int
		wantErr   string
	}{
		{
			name:      "valid with 1 peer",
			txnID:     42,
			data:      buildAnnounceResponse(42, 900, 0, 1, []byte{10, 0, 0, 1, 0x1a, 0xe1}),
			wantPeers: 1,
		},
		{
			name:      "valid with 0 peers",
			txnID:     42,
			data:      buildAnnounceResponse(42, 900, 0, 0, nil),
			wantPeers: 0,
		},
		{
			name:    "too short",
			txnID:   42,
			data:    []byte{0, 0, 0, 1},
			wantErr: "too short",
		},
		{
			name:    "wrong action",
			txnID:   42,
			data:    buildAnnounceResponse(42, 900, 0, 0, nil),
			wantErr: "unexpected action",
		},
		{
			name:    "wrong txn ID",
			txnID:   42,
			data:    buildAnnounceResponse(99, 900, 0, 0, nil),
			wantErr: "transaction ID mismatch",
		},
		{
			name:    "error action",
			txnID:   42,
			data:    buildErrorResponse(42, "banned"),
			wantErr: "banned",
		},
	}

	wrongAction := buildAnnounceResponse(42, 900, 0, 0, nil)
	binary.BigEndian.PutUint32(wrongAction[0:4], ActionScrape)
	tests[3].data = wrongAction

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := unmarshalUDPAnnounceResponse(tt.txnID, tt.data)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				// Verify structured error types
				switch tt.name {
				case "too short", "wrong action", "wrong txn ID":
					var re *ResponseError
					if !errors.As(err, &re) {
						t.Errorf("expected *ResponseError, got %T", err)
					}
				case "error action":
					var te *TrackerError
					if !errors.As(err, &te) {
						t.Errorf("expected *TrackerError, got %T", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Peers) != tt.wantPeers {
				t.Errorf("got %d peers, want %d", len(resp.Peers), tt.wantPeers)
			}
		})
	}
}

// marshalUDPScrapeRequest

func TestMarshalUDPScrapeRequest(t *testing.T) {
	tracker := &UDPTracker{
		conn: UDPTrackerConn{connectionID: 0xBEEFCAFE},
	}

	h1 := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	h2 := [20]byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	t.Run("1 hash", func(t *testing.T) {
		data, _ := tracker.marshalUDPScrapeRequest([][20]byte{h1})
		if len(data) != 36 {
			t.Fatalf("expected 36 bytes, got %d", len(data))
		}
		connID := binary.BigEndian.Uint64(data[0:8])
		if connID != 0xBEEFCAFE {
			t.Errorf("connectionID = 0x%x, want 0xBEEFCAFE", connID)
		}
		action := binary.BigEndian.Uint32(data[8:12])
		if action != ActionScrape {
			t.Errorf("action = %d, want %d", action, ActionScrape)
		}
		if !bytes.Equal(data[16:36], h1[:]) {
			t.Error("hash mismatch")
		}
	})

	t.Run("2 hashes", func(t *testing.T) {
		data, _ := tracker.marshalUDPScrapeRequest([][20]byte{h1, h2})
		if len(data) != 56 {
			t.Fatalf("expected 56 bytes, got %d", len(data))
		}
		if !bytes.Equal(data[16:36], h1[:]) {
			t.Error("hash 1 mismatch")
		}
		if !bytes.Equal(data[36:56], h2[:]) {
			t.Error("hash 2 mismatch")
		}
	})
}

// unmarshalUDPScrapeResponse

func TestUnmarshalUDPScrapeResponse(t *testing.T) {
	hash := [20]byte{0xAA}

	t.Run("valid", func(t *testing.T) {
		data := buildScrapeResponse(42, []FileStats{{Seeders: 10, Completed: 50, Leechers: 3}})
		resp, err := unmarshalUDPScrapeResponse(42, [][20]byte{hash}, data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		stats := resp[hash]
		if stats.Seeders != 10 || stats.Completed != 50 || stats.Leechers != 3 {
			t.Errorf("stats = %+v, want {10, 50, 3}", stats)
		}
	})

	t.Run("too short", func(t *testing.T) {
		_, err := unmarshalUDPScrapeResponse(42, [][20]byte{hash}, []byte{0, 0})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("wrong action", func(t *testing.T) {
		data := buildScrapeResponse(42, nil)
		binary.BigEndian.PutUint32(data[0:4], ActionConnect)
		_, err := unmarshalUDPScrapeResponse(42, [][20]byte{hash}, data)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("wrong txn ID", func(t *testing.T) {
		data := buildScrapeResponse(99, nil)
		_, err := unmarshalUDPScrapeResponse(42, [][20]byte{hash}, data)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// connectionExpired

func TestConnectionExpired(t *testing.T) {
	t.Run("fresh connection", func(t *testing.T) {
		tracker := &UDPTracker{conn: UDPTrackerConn{connectedAt: time.Now()}}
		if tracker.connectionExpired() {
			t.Error("fresh connection should not be expired")
		}
	})

	t.Run("old connection", func(t *testing.T) {
		tracker := &UDPTracker{conn: UDPTrackerConn{connectedAt: time.Now().Add(-61 * time.Second)}}
		if !tracker.connectionExpired() {
			t.Error("61s old connection should be expired")
		}
	})

	t.Run("zero time", func(t *testing.T) {
		tracker := &UDPTracker{}
		if tracker.connectionExpired() {
			t.Error("zero connectedAt should not be expired")
		}
	})
}

// getDeadline

func TestGetDeadline(t *testing.T) {
	tests := []struct {
		n       int
		wantSec float64
	}{
		{0, 15},
		{1, 30},
		{2, 60},
		{3, 120},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("n=%d", tt.n), func(t *testing.T) {
			tracker := &UDPTracker{conn: UDPTrackerConn{n: tt.n}}
			before := time.Now()
			deadline := tracker.getDeadline()
			diff := deadline.Sub(before).Seconds()

			// Allow 1 second tolerance
			if diff < tt.wantSec-1 || diff > tt.wantSec+1 {
				t.Errorf("deadline diff = %.1fs, want ~%.0fs", diff, tt.wantSec)
			}
		})
	}
}

// UDP Integration Tests (mock server)

// startMockUDPTracker starts a UDP server that processes incoming requests using
// the provided handler. The handler receives raw request bytes and returns
// response bytes. Returns the server address and a cleanup function.
func startMockUDPTracker(t *testing.T, handler func([]byte, net.Addr) []byte) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, 4096)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // connection closed
			}
			resp := handler(buf[:n], addr)
			if resp != nil {
				conn.WriteToUDP(resp, addr)
			}
		}
	}()

	return conn.LocalAddr().(*net.UDPAddr)
}

func TestUDPTrackerConnectAndAnnounce(t *testing.T) {
	var connID uint64 = 0x1234567890ABCDEF
	var mu sync.Mutex
	requestCount := 0

	addr := startMockUDPTracker(t, func(data []byte, _ net.Addr) []byte {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()

		action := binary.BigEndian.Uint32(data[8:12])
		txnID := binary.BigEndian.Uint32(data[12:16])

		switch action {
		case ActionConnect:
			return buildConnectResponse(ActionConnect, txnID, connID)
		case ActionAnnounce:
			// Verify the request uses our connection ID
			gotConnID := binary.BigEndian.Uint64(data[0:8])
			if gotConnID != connID {
				t.Errorf("request %d: connectionID = 0x%x, want 0x%x", count, gotConnID, connID)
			}
			peers := []byte{192, 168, 1, 1, 0x1a, 0xe1, 10, 0, 0, 1, 0x1f, 0x90}
			return buildAnnounceResponse(txnID, 1800, 1, 1, peers)
		}
		return nil
	})

	tracker, err := newUDPTracker(addr.String())
	if err != nil {
		t.Fatalf("newUDPTracker: %v", err)
	}

	resp, err := tracker.Announce(AnnounceRequest{
		InfoHash: [20]byte{0xAA},
		Port:     6881,
	})
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if resp.Interval != 1800*time.Second {
		t.Errorf("interval = %v, want 1800s", resp.Interval)
	}
	if len(resp.Peers) != 2 {
		t.Errorf("got %d peers, want 2", len(resp.Peers))
	}
}

func TestUDPTrackerErrorResponse(t *testing.T) {
	addr := startMockUDPTracker(t, func(data []byte, _ net.Addr) []byte {
		txnID := binary.BigEndian.Uint32(data[12:16])
		action := binary.BigEndian.Uint32(data[8:12])
		if action == ActionConnect {
			return buildConnectResponse(ActionConnect, txnID, 0x1234)
		}
		// Return error for announce
		return buildErrorResponse(txnID, "info hash not found")
	})

	tracker, err := newUDPTracker(addr.String())
	if err != nil {
		t.Fatalf("newUDPTracker: %v", err)
	}

	_, err = tracker.Announce(AnnounceRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	var te *TrackerError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TrackerError, got %T: %v", err, err)
	}
	if te.Message != "info hash not found" {
		t.Errorf("TrackerError.Message = %q, want %q", te.Message, "info hash not found")
	}
}

func TestUDPTrackerScrape(t *testing.T) {
	var connID uint64 = 0xABCDEF
	hash := [20]byte{0xBB}

	addr := startMockUDPTracker(t, func(data []byte, _ net.Addr) []byte {
		action := binary.BigEndian.Uint32(data[8:12])
		txnID := binary.BigEndian.Uint32(data[12:16])

		switch action {
		case ActionConnect:
			return buildConnectResponse(ActionConnect, txnID, connID)
		case ActionScrape:
			return buildScrapeResponse(txnID, []FileStats{{Seeders: 42, Completed: 100, Leechers: 7}})
		}
		return nil
	})

	tracker, err := newUDPTracker(addr.String())
	if err != nil {
		t.Fatalf("newUDPTracker: %v", err)
	}

	resp, err := tracker.Scrape([][20]byte{hash})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	stats, ok := resp[hash]
	if !ok {
		t.Fatal("hash not found in scrape response")
	}
	if stats.Seeders != 42 || stats.Completed != 100 || stats.Leechers != 7 {
		t.Errorf("stats = %+v, want {42, 100, 7}", stats)
	}
}

// Test helpers

func buildConnectResponse(action uint32, txnID uint32, connID uint64) []byte {
	resp := make([]byte, 16)
	binary.BigEndian.PutUint32(resp[0:4], action)
	binary.BigEndian.PutUint32(resp[4:8], txnID)
	binary.BigEndian.PutUint64(resp[8:16], connID)
	return resp
}

func buildAnnounceResponse(txnID uint32, interval uint32, leechers uint32, seeders uint32, peerData []byte) []byte {
	resp := make([]byte, 20+len(peerData))
	binary.BigEndian.PutUint32(resp[0:4], ActionAnnounce)
	binary.BigEndian.PutUint32(resp[4:8], txnID)
	binary.BigEndian.PutUint32(resp[8:12], interval)
	binary.BigEndian.PutUint32(resp[12:16], leechers)
	binary.BigEndian.PutUint32(resp[16:20], seeders)
	copy(resp[20:], peerData)
	return resp
}

func buildScrapeResponse(txnID uint32, stats []FileStats) []byte {
	resp := make([]byte, 8+12*len(stats))
	binary.BigEndian.PutUint32(resp[0:4], ActionScrape)
	binary.BigEndian.PutUint32(resp[4:8], txnID)
	for i, s := range stats {
		off := 8 + i*12
		binary.BigEndian.PutUint32(resp[off:off+4], s.Seeders)
		binary.BigEndian.PutUint32(resp[off+4:off+8], s.Completed)
		binary.BigEndian.PutUint32(resp[off+8:off+12], s.Leechers)
	}
	return resp
}

func buildErrorResponse(txnID uint32, msg string) []byte {
	resp := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint32(resp[0:4], ActionError)
	binary.BigEndian.PutUint32(resp[4:8], txnID)
	copy(resp[8:], []byte(msg))
	return resp
}

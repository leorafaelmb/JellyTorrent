package tracker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestUrlEncodeInfoHash(t *testing.T) {
	// "aabbcc" -> "%aa%bb%cc"
	got := urlEncodeInfoHash("aabbcc")
	want := "%aa%bb%cc"
	if got != want {
		t.Errorf("urlEncodeInfoHash(\"aabbcc\") = %q, want %q", got, want)
	}
}

func TestUrlEncodeInfoHashFull(t *testing.T) {
	hex := "0102030405060708090a0b0c0d0e0f1011121314"
	got := urlEncodeInfoHash(hex)
	if len(got) != 60 { // 20 * 3 ("%xx")
		t.Errorf("expected length 60, got %d", len(got))
	}
	if !strings.HasPrefix(got, "%01%02") {
		t.Errorf("unexpected prefix: %s", got[:12])
	}
}

func TestUnmarshalPeerAddresses(t *testing.T) {
	// Two peers: 192.168.1.1:6881 and 10.0.0.1:8080
	data := make([]byte, 12)
	data[0], data[1], data[2], data[3] = 192, 168, 1, 1
	binary.BigEndian.PutUint16(data[4:6], 6881)
	data[6], data[7], data[8], data[9] = 10, 0, 0, 1
	binary.BigEndian.PutUint16(data[10:12], 8080)

	peers, err := unmarshalPeerAddresses(2, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	want0 := netip.MustParseAddrPort("192.168.1.1:6881")
	want1 := netip.MustParseAddrPort("10.0.0.1:8080")
	if peers[0] != want0 {
		t.Errorf("peer[0] = %s, want %s", peers[0], want0)
	}
	if peers[1] != want1 {
		t.Errorf("peer[1] = %s, want %s", peers[1], want1)
	}
}

func TestUnmarshalPeerAddressesEmpty(t *testing.T) {
	peers, err := unmarshalPeerAddresses(0, []byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(peers))
	}
}

func TestNewTrackerHTTP(t *testing.T) {
	tr, err := NewTracker("http://tracker.example.com/announce")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tr.(*HTTPTracker); !ok {
		t.Errorf("expected *HTTPTracker, got %T", tr)
	}
}

func TestNewTrackerHTTPS(t *testing.T) {
	tr, err := NewTracker("https://tracker.example.com/announce")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := tr.(*HTTPTracker); !ok {
		t.Errorf("expected *HTTPTracker, got %T", tr)
	}
}

func TestNewTrackerUnsupported(t *testing.T) {
	_, err := NewTracker("ftp://tracker.example.com/announce")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Errorf("expected ErrUnsupportedScheme, got: %v", err)
	}
}

// HTTP marshalHTTPAnnounceRequest

func TestMarshalHTTPAnnounceRequest(t *testing.T) {
	tracker := newHTTPTracker("http://tracker.example.com/announce")

	t.Run("compact=1 and no event for EventNone", func(t *testing.T) {
		req := AnnounceRequest{Event: EventNone}
		url := tracker.marshalHTTPAnnounceRequest(req)
		if !strings.Contains(url, "compact=1") {
			t.Error("URL should contain compact=1")
		}
		if strings.Contains(url, "event=") {
			t.Error("URL should not contain event= for EventNone")
		}
	})

	t.Run("event=started for EventStarted", func(t *testing.T) {
		req := AnnounceRequest{Event: EventStarted}
		url := tracker.marshalHTTPAnnounceRequest(req)
		if !strings.Contains(url, "event=started") {
			t.Errorf("URL should contain event=started, got: %s", url)
		}
	})

	t.Run("event=completed for EventCompleted", func(t *testing.T) {
		req := AnnounceRequest{Event: EventCompleted}
		url := tracker.marshalHTTPAnnounceRequest(req)
		if !strings.Contains(url, "event=completed") {
			t.Errorf("URL should contain event=completed, got: %s", url)
		}
	})

	t.Run("event=stopped for EventStopped", func(t *testing.T) {
		req := AnnounceRequest{Event: EventStopped}
		url := tracker.marshalHTTPAnnounceRequest(req)
		if !strings.Contains(url, "event=stopped") {
			t.Errorf("URL should contain event=stopped, got: %s", url)
		}
	})

	t.Run("info hash is percent-encoded", func(t *testing.T) {
		req := AnnounceRequest{
			InfoHash: [20]byte{0x01, 0x02, 0x03},
		}
		url := tracker.marshalHTTPAnnounceRequest(req)
		if !strings.Contains(url, "%01%02%03") {
			t.Errorf("info hash not percent-encoded in URL: %s", url)
		}
	})
}

// HTTP unmarshalHTTPAnnounceResponse

func TestUnmarshalHTTPAnnounceResponse(t *testing.T) {
	t.Run("valid compact response", func(t *testing.T) {
		// Bencode: d8:intervali900e5:peers6:<6 bytes>e
		peer := []byte{192, 168, 1, 1}
		port := []byte{0x1a, 0xe1} // 6881
		peerData := append(peer, port...)
		bencoded := fmt.Sprintf("d8:intervali900e5:peers6:%se", string(peerData))

		resp, err := unmarshalHTTPAnnounceResponse([]byte(bencoded))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Interval != 900*time.Second {
			t.Errorf("interval = %v, want 900s", resp.Interval)
		}
		if len(resp.Peers) != 1 {
			t.Fatalf("expected 1 peer, got %d", len(resp.Peers))
		}
		want := netip.MustParseAddrPort("192.168.1.1:6881")
		if resp.Peers[0] != want {
			t.Errorf("peer = %s, want %s", resp.Peers[0], want)
		}
	})

	t.Run("failure reason", func(t *testing.T) {
		bencoded := "d14:failure reason11:not allowede"
		_, err := unmarshalHTTPAnnounceResponse([]byte(bencoded))
		if err == nil {
			t.Fatal("expected error")
		}
		var te *TrackerError
		if !errors.As(err, &te) {
			t.Fatalf("expected *TrackerError, got %T: %v", err, err)
		}
		if te.Message != "not allowed" {
			t.Errorf("TrackerError.Message = %q, want %q", te.Message, "not allowed")
		}
	})

	t.Run("missing interval", func(t *testing.T) {
		bencoded := "d5:peers0:e"
		_, err := unmarshalHTTPAnnounceResponse([]byte(bencoded))
		if err == nil {
			t.Fatal("expected error for missing interval")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})

	t.Run("invalid bencode", func(t *testing.T) {
		_, err := unmarshalHTTPAnnounceResponse([]byte("not bencode"))
		if err == nil {
			t.Fatal("expected error for invalid bencode")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})
}

// unmarshalBencodedPeers

func TestUnmarshalBencodedPeers(t *testing.T) {
	t.Run("compact bytes", func(t *testing.T) {
		data := make([]byte, 6)
		data[0], data[1], data[2], data[3] = 10, 0, 0, 1
		binary.BigEndian.PutUint16(data[4:6], 51413)

		peers, err := unmarshalBencodedPeers(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(peers) != 1 {
			t.Fatalf("expected 1 peer, got %d", len(peers))
		}
		want := netip.MustParseAddrPort("10.0.0.1:51413")
		if peers[0] != want {
			t.Errorf("peer = %s, want %s", peers[0], want)
		}
	})

	t.Run("compact string", func(t *testing.T) {
		data := make([]byte, 6)
		data[0], data[1], data[2], data[3] = 10, 0, 0, 1
		binary.BigEndian.PutUint16(data[4:6], 51413)

		peers, err := unmarshalBencodedPeers(string(data))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(peers) != 1 {
			t.Fatalf("expected 1 peer, got %d", len(peers))
		}
	})

	t.Run("dict format", func(t *testing.T) {
		peerList := []interface{}{
			map[string]interface{}{
				"ip":   "192.168.1.100",
				"port": 6881,
			},
		}
		peers, err := unmarshalBencodedPeers(peerList)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(peers) != 1 {
			t.Fatalf("expected 1 peer, got %d", len(peers))
		}
		want := netip.MustParseAddrPort("192.168.1.100:6881")
		if peers[0] != want {
			t.Errorf("peer = %s, want %s", peers[0], want)
		}
	})

	t.Run("dict missing ip", func(t *testing.T) {
		peerList := []interface{}{
			map[string]interface{}{"port": 6881},
		}
		_, err := unmarshalBencodedPeers(peerList)
		if err == nil {
			t.Fatal("expected error for missing ip")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})

	t.Run("dict missing port", func(t *testing.T) {
		peerList := []interface{}{
			map[string]interface{}{"ip": "1.2.3.4"},
		}
		_, err := unmarshalBencodedPeers(peerList)
		if err == nil {
			t.Fatal("expected error for missing port")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		_, err := unmarshalBencodedPeers(42)
		if err == nil {
			t.Fatal("expected error for int type")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
		if !strings.Contains(re.Reason, "int") {
			t.Errorf("Reason should mention type, got: %s", re.Reason)
		}
	})

	t.Run("nil", func(t *testing.T) {
		_, err := unmarshalBencodedPeers(nil)
		if err == nil {
			t.Fatal("expected error for nil")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})
}

// --- unmarshalHTTPScrapeResponse ---

func TestUnmarshalHTTPScrapeResponse(t *testing.T) {
	t.Run("valid response", func(t *testing.T) {
		// Build a bencoded scrape response:
		// d5:filesd<20-byte hash>d8:completei10e10:downloadedi50e10:incompletei3eeee
		infoHash := strings.Repeat("A", 20) // 20 'A' bytes
		bencoded := fmt.Sprintf("d5:filesd20:%sd8:completei10e10:downloadedi50e10:incompletei3eeee", infoHash)

		resp, err := unmarshalHTTPScrapeResponse([]byte(bencoded))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var key [20]byte
		copy(key[:], []byte(infoHash))
		stats, ok := resp[key]
		if !ok {
			t.Fatal("info hash not found in response")
		}
		if stats.Seeders != 10 {
			t.Errorf("seeders = %d, want 10", stats.Seeders)
		}
		if stats.Completed != 50 {
			t.Errorf("completed = %d, want 50", stats.Completed)
		}
		if stats.Leechers != 3 {
			t.Errorf("leechers = %d, want 3", stats.Leechers)
		}
	})

	t.Run("missing files key", func(t *testing.T) {
		bencoded := "d4:infod3:fooi1eee"
		_, err := unmarshalHTTPScrapeResponse([]byte(bencoded))
		if err == nil {
			t.Fatal("expected error for missing files key")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})

	t.Run("invalid bencode", func(t *testing.T) {
		_, err := unmarshalHTTPScrapeResponse([]byte("garbage"))
		if err == nil {
			t.Fatal("expected error for invalid bencode")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})
}

// --- HTTP Integration Tests ---

func TestHTTPTrackerAnnounce(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		peer := []byte{10, 0, 0, 1, 0x1a, 0xe1} // 10.0.0.1:6881
		body := fmt.Sprintf("d8:intervali1800e5:peers6:%se", string(peer))

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify compact=1 is in query
			if r.URL.Query().Get("compact") != "1" {
				t.Error("expected compact=1 in query")
			}
			w.Write([]byte(body))
		}))
		defer server.Close()

		tracker := newHTTPTracker(server.URL + "/announce")
		resp, err := tracker.Announce(AnnounceRequest{
			Port:  6881,
			Event: EventNone,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Interval != 1800*time.Second {
			t.Errorf("interval = %v, want 1800s", resp.Interval)
		}
		if len(resp.Peers) != 1 {
			t.Fatalf("expected 1 peer, got %d", len(resp.Peers))
		}
	})

	t.Run("HTTP 500", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte("internal server error"))
		}))
		defer server.Close()

		tracker := newHTTPTracker(server.URL + "/announce")
		_, err := tracker.Announce(AnnounceRequest{})
		if err == nil {
			t.Fatal("expected error for HTTP 500")
		}
		var te *TrackerError
		if !errors.As(err, &te) {
			t.Fatalf("expected *TrackerError, got %T: %v", err, err)
		}
		if !strings.Contains(te.Message, "500") {
			t.Errorf("TrackerError.Message should mention status code, got: %s", te.Message)
		}
	})

	t.Run("failure reason", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("d14:failure reason16:invalid infohashe"))
		}))
		defer server.Close()

		tracker := newHTTPTracker(server.URL + "/announce")
		_, err := tracker.Announce(AnnounceRequest{})
		if err == nil {
			t.Fatal("expected error for failure reason")
		}
		var te *TrackerError
		if !errors.As(err, &te) {
			t.Fatalf("expected *TrackerError, got %T: %v", err, err)
		}
		if te.Message != "invalid infohash" {
			t.Errorf("TrackerError.Message = %q, want %q", te.Message, "invalid infohash")
		}
	})
}

func TestHTTPTrackerScrape(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		infoHash := strings.Repeat("B", 20)
		body := fmt.Sprintf("d5:filesd20:%sd8:completei5e10:downloadedi20e10:incompletei2eeee", infoHash)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		defer server.Close()

		tracker := newHTTPTracker(server.URL + "/announce")
		hashes := [][20]byte{}
		var h [20]byte
		copy(h[:], infoHash)
		hashes = append(hashes, h)

		resp, err := tracker.Scrape(hashes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		stats, ok := resp[h]
		if !ok {
			t.Fatal("info hash not found in scrape response")
		}
		if stats.Seeders != 5 {
			t.Errorf("seeders = %d, want 5", stats.Seeders)
		}
	})

	t.Run("no announce in URL", func(t *testing.T) {
		tracker := newHTTPTracker("http://example.com/something")
		_, err := tracker.Scrape([][20]byte{{}})
		if err == nil {
			t.Fatal("expected error for URL without 'announce'")
		}
		var re *ResponseError
		if !errors.As(err, &re) {
			t.Fatalf("expected *ResponseError, got %T: %v", err, err)
		}
	})
}

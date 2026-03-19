package peer

import (
	"net/netip"
	"testing"
)

func TestCompactPeersRoundTrip(t *testing.T) {
	addrs := []netip.AddrPort{
		netip.MustParseAddrPort("192.168.1.1:6881"),
		netip.MustParseAddrPort("10.0.0.1:51413"),
		netip.MustParseAddrPort("172.16.0.100:8080"),
	}

	data := CompactPeers(addrs)

	if len(data) != 18 { // 3 * 6
		t.Fatalf("expected 18 bytes, got %d", len(data))
	}

	parsed := ParseCompactPeers(data)

	if len(parsed) != len(addrs) {
		t.Fatalf("expected %d peers, got %d", len(addrs), len(parsed))
	}

	for i, addr := range addrs {
		if parsed[i] != addr {
			t.Errorf("peer %d: expected %s, got %s", i, addr, parsed[i])
		}
	}
}

func TestCompactPeersEmpty(t *testing.T) {
	data := CompactPeers(nil)
	if len(data) != 0 {
		t.Fatalf("expected empty bytes, got %d", len(data))
	}

	parsed := ParseCompactPeers(nil)
	if len(parsed) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(parsed))
	}
}

func TestParseCompactPeersTrailingBytes(t *testing.T) {
	// 7 bytes = 1 full peer + 1 trailing byte (should be ignored)
	addrs := []netip.AddrPort{netip.MustParseAddrPort("1.2.3.4:5678")}
	data := append(CompactPeers(addrs), 0xFF)

	parsed := ParseCompactPeers(data)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(parsed))
	}
	if parsed[0] != addrs[0] {
		t.Errorf("expected %s, got %s", addrs[0], parsed[0])
	}
}

func TestPEXMessageRoundTrip(t *testing.T) {
	msg := &PEXMessage{
		Added: []netip.AddrPort{
			netip.MustParseAddrPort("192.168.1.1:6881"),
			netip.MustParseAddrPort("10.0.0.1:51413"),
		},
		AddedF:  []byte{0x00, 0x02}, // second peer is seed
		Dropped: []netip.AddrPort{netip.MustParseAddrPort("172.16.0.1:8080")},
	}

	encoded, err := EncodePEXMessage(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	parsed, err := ParsePEXMessage(encoded)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(parsed.Added) != 2 {
		t.Fatalf("expected 2 added, got %d", len(parsed.Added))
	}
	if len(parsed.Dropped) != 1 {
		t.Fatalf("expected 1 dropped, got %d", len(parsed.Dropped))
	}

	for i, addr := range msg.Added {
		if parsed.Added[i] != addr {
			t.Errorf("added[%d]: expected %s, got %s", i, addr, parsed.Added[i])
		}
	}

	if parsed.Dropped[0] != msg.Dropped[0] {
		t.Errorf("dropped[0]: expected %s, got %s", msg.Dropped[0], parsed.Dropped[0])
	}

	if len(parsed.AddedF) != 2 || parsed.AddedF[1] != 0x02 {
		t.Errorf("expected flags [0x00, 0x02], got %v", parsed.AddedF)
	}
}

func TestPEXMessageEmptyFields(t *testing.T) {
	msg := &PEXMessage{
		Added:   nil,
		AddedF:  nil,
		Dropped: nil,
	}

	encoded, err := EncodePEXMessage(msg)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}

	parsed, err := ParsePEXMessage(encoded)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(parsed.Added) != 0 {
		t.Errorf("expected 0 added, got %d", len(parsed.Added))
	}
	if len(parsed.Dropped) != 0 {
		t.Errorf("expected 0 dropped, got %d", len(parsed.Dropped))
	}
}

func TestParsePEXMessageInvalidInput(t *testing.T) {
	// Not valid bencode
	_, err := ParsePEXMessage([]byte("not bencode"))
	if err == nil {
		t.Error("expected error for invalid bencode")
	}

	// Valid bencode but not a dict
	_, err = ParsePEXMessage([]byte("i42e"))
	if err == nil {
		t.Error("expected error for non-dict")
	}
}

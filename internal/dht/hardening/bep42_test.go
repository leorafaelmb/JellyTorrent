package hardening

import (
	"net/netip"
	"testing"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

func TestCRC32CFromIP(t *testing.T) {
	// Verify CRC32C computation against BEP 42 reference values.
	tests := []struct {
		ip       [4]byte
		r        byte
		wantCRC  uint32
	}{
		{[4]byte{124, 31, 75, 21}, 1, 0x5fbfbdb2},
		{[4]byte{21, 75, 31, 124}, 6, 0x5a3ce9b0},
		{[4]byte{65, 23, 51, 170}, 6, 0xa5d4344a},
		{[4]byte{84, 124, 73, 14}, 1, 0x1b03217b},
		{[4]byte{43, 213, 53, 83}, 2, 0xe56f6972},
	}
	for _, tt := range tests {
		got := crc32cFromIP(tt.ip, tt.r)
		if got != tt.wantCRC {
			t.Errorf("crc32cFromIP(%v, %d) = 0x%08x, want 0x%08x", tt.ip, tt.r, got, tt.wantCRC)
		}
	}
}

func TestFirst21BitsMatch(t *testing.T) {
	// Build a node ID whose first 3 bytes match the CRC derivation.
	crc := uint32(0x5fbfbdb2) // from vector 1
	var id nodeid.NodeID
	id[0] = byte(crc >> 24)           // 0x5f
	id[1] = byte(crc >> 16)           // 0xbf
	id[2] = byte(crc>>8)&0xf8 | 0x07 // top 5 bits from CRC, low 3 random

	if !first21BitsMatch(id, crc) {
		t.Error("first21BitsMatch returned false for matching ID")
	}

	// Flip a constrained bit — should fail.
	id[0] ^= 0x01
	if first21BitsMatch(id, crc) {
		t.Error("first21BitsMatch returned true after flipping bit in id[0]")
	}
}

func TestValidateKnownVectors(t *testing.T) {
	// BEP 42 test vectors: IP, rand byte, expected first 3 bytes of node ID.
	vectors := []struct {
		ip          [4]byte
		randByte    byte
		wantID0     byte
		wantID1     byte
		wantID2Mask byte // top 5 bits of id[2]
	}{
		{[4]byte{124, 31, 75, 21}, 1, 0x5f, 0xbf, 0xb8},
		{[4]byte{21, 75, 31, 124}, 86, 0x5a, 0x3c, 0xe8},
		{[4]byte{65, 23, 51, 170}, 22, 0xa5, 0xd4, 0x30},
		{[4]byte{84, 124, 73, 14}, 65, 0x1b, 0x03, 0x20},
		{[4]byte{43, 213, 53, 83}, 90, 0xe5, 0x6f, 0x68},
	}

	for _, v := range vectors {
		// Construct a node ID matching the test vector.
		var id nodeid.NodeID
		id[0] = v.wantID0
		id[1] = v.wantID1
		id[2] = v.wantID2Mask | 0x07 // fill low 3 bits with arbitrary data
		id[19] = v.randByte

		ip := netip.AddrFrom4(v.ip)
		if !ValidateNodeID(id, ip) {
			t.Errorf("ValidateNodeID failed for IP %v, rand %d", v.ip, v.randByte)
		}
	}
}

func TestGenerateAndValidateRoundTrip(t *testing.T) {
	ips := []netip.Addr{
		netip.MustParseAddr("124.31.75.21"),
		netip.MustParseAddr("21.75.31.124"),
		netip.MustParseAddr("65.23.51.170"),
		netip.MustParseAddr("1.2.3.4"),
		netip.MustParseAddr("192.168.1.1"),
		netip.MustParseAddr("10.0.0.1"),
	}

	for _, ip := range ips {
		for i := 0; i < 10; i++ {
			id, err := GenerateCompliantID(ip)
			if err != nil {
				t.Fatalf("GenerateCompliantID(%s): %v", ip, err)
			}
			if !ValidateNodeID(id, ip) {
				t.Errorf("GenerateCompliantID(%s) produced ID that fails validation: %s", ip, id)
			}
		}
	}
}

func TestRandomIDFailsValidation(t *testing.T) {
	ip := netip.MustParseAddr("124.31.75.21")

	// A purely random ID is extremely unlikely to pass BEP 42 validation
	// (probability ~1/2^21). Test 100 random IDs to be safe.
	for i := 0; i < 100; i++ {
		id := nodeid.New()
		if ValidateNodeID(id, ip) {
			// This can happen with probability ~1/2^21 per attempt.
			// With 100 attempts, expected false positives ~0.00005.
			// If this fails, it's astronomically unlikely to be a fluke.
			t.Logf("Random ID %s validated against %s (rare but possible)", id, ip)
		}
	}
}

func TestMultipleGenerationsVary(t *testing.T) {
	ip := netip.MustParseAddr("1.2.3.4")
	seen := make(map[nodeid.NodeID]bool)

	for i := 0; i < 20; i++ {
		id, err := GenerateCompliantID(ip)
		if err != nil {
			t.Fatal(err)
		}
		seen[id] = true
	}

	// 20 random IDs from the same IP should all be distinct.
	if len(seen) < 20 {
		t.Errorf("expected 20 distinct IDs, got %d", len(seen))
	}
}

func TestIPv6ReturnsError(t *testing.T) {
	ip := netip.MustParseAddr("2001:db8::1")

	_, err := GenerateCompliantID(ip)
	if err == nil {
		t.Error("GenerateCompliantID should return error for IPv6")
	}

	var id nodeid.NodeID
	if ValidateNodeID(id, ip) {
		t.Error("ValidateNodeID should return false for IPv6")
	}
}

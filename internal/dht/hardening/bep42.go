package hardening

import (
	"crypto/rand"
	"fmt"
	"hash/crc32"
	"net/netip"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
)

var (
	v4Mask          = [4]byte{0x03, 0x0f, 0x3f, 0xff}
	castagnoliTable = crc32.MakeTable(crc32.Castagnoli)
)

// GenerateCompliantID creates a BEP 42 compliant node ID whose first 21 bits
// are derived from the given IPv4 address via CRC32C. Bytes 3..18 are random,
// and byte 19 stores the random byte used in the derivation.
func GenerateCompliantID(ip netip.Addr) (nodeid.NodeID, error) {
	ip = ip.Unmap()
	if !ip.Is4() {
		return nodeid.NodeID{}, fmt.Errorf("BEP 42: only IPv4 supported, got %s", ip)
	}

	var id nodeid.NodeID

	// Fill all bytes with random data first.
	if _, err := rand.Read(id[:]); err != nil {
		return nodeid.NodeID{}, fmt.Errorf("BEP 42: random generation failed: %w", err)
	}

	// id[19] is the random byte; r is the low 3 bits used in the CRC input.
	randByte := id[19]
	r := randByte & 0x07

	crc := crc32cFromIP(ip.As4(), r)

	// Constrain the first 21 bits of the node ID to match the CRC.
	id[0] = byte(crc >> 24)
	id[1] = byte(crc >> 16)
	id[2] = byte(crc>>8)&0xf8 | id[2]&0x07

	return id, nil
}

// ValidateNodeID checks whether the first 21 bits of id match the CRC32C
// derivation from ip per BEP 42. Returns false for non-IPv4 addresses.
func ValidateNodeID(id nodeid.NodeID, ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.Is4() {
		return false
	}

	r := id[19] & 0x07
	crc := crc32cFromIP(ip.As4(), r)
	return first21BitsMatch(id, crc)
}

// crc32cFromIP computes the CRC32C value for the given IPv4 address and
// random value r (0..7), following the BEP 42 masking algorithm.
func crc32cFromIP(ip4 [4]byte, r byte) uint32 {
	masked := [4]byte{
		ip4[0]&v4Mask[0] | r<<5,
		ip4[1] & v4Mask[1],
		ip4[2] & v4Mask[2],
		ip4[3] & v4Mask[3],
	}
	return crc32.Checksum(masked[:], castagnoliTable)
}

// first21BitsMatch checks whether the first 21 bits of a node ID match
// the first 21 bits of the CRC32C result.
func first21BitsMatch(id nodeid.NodeID, crc uint32) bool {
	if id[0] != byte(crc>>24) {
		return false
	}
	if id[1] != byte(crc>>16) {
		return false
	}
	if id[2]&0xf8 != byte(crc>>8)&0xf8 {
		return false
	}
	return true
}

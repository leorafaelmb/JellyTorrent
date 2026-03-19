package peer

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
)

// PEXMessage represents a parsed PEX (Peer Exchange) message per BEP 11.
type PEXMessage struct {
	Added   []netip.AddrPort
	AddedF  []byte // one flag byte per added peer
	Dropped []netip.AddrPort
}

// CompactPeers encodes a list of AddrPort into compact 6-byte-per-peer format
// (4 bytes IPv4 + 2 bytes big-endian port).
func CompactPeers(addrs []netip.AddrPort) []byte {
	buf := make([]byte, len(addrs)*6)
	for i, a := range addrs {
		ip := a.Addr().As4()
		copy(buf[i*6:i*6+4], ip[:])
		binary.BigEndian.PutUint16(buf[i*6+4:i*6+6], a.Port())
	}
	return buf
}

// ParseCompactPeers decodes compact 6-byte-per-peer data into AddrPort slice.
// Silently skips any trailing bytes that don't form a complete 6-byte entry.
func ParseCompactPeers(data []byte) []netip.AddrPort {
	numPeers := len(data) / 6
	peers := make([]netip.AddrPort, 0, numPeers)
	for i := 0; i < numPeers; i++ {
		offset := i * 6
		ip := netip.AddrFrom4([4]byte(data[offset : offset+4]))
		port := binary.BigEndian.Uint16(data[offset+4 : offset+6])
		peers = append(peers, netip.AddrPortFrom(ip, port))
	}
	return peers
}

// EncodePEXMessage encodes a PEX message into a bencoded dictionary.
func EncodePEXMessage(msg *PEXMessage) ([]byte, error) {
	dict := map[string]interface{}{
		"added":   string(CompactPeers(msg.Added)),
		"added.f": string(msg.AddedF),
		"dropped": string(CompactPeers(msg.Dropped)),
	}
	return bencode.Encode(dict)
}

// ParsePEXMessage decodes a bencoded PEX payload into a PEXMessage.
func ParsePEXMessage(data []byte) (*PEXMessage, error) {
	decoded, err := bencode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode PEX message: %w", err)
	}

	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("PEX message is not a dictionary")
	}

	msg := &PEXMessage{}

	if added, ok := extractBytes(dict, "added"); ok {
		msg.Added = ParseCompactPeers(added)
	}

	if addedF, ok := extractBytes(dict, "added.f"); ok {
		msg.AddedF = addedF
	}

	if dropped, ok := extractBytes(dict, "dropped"); ok {
		msg.Dropped = ParseCompactPeers(dropped)
	}

	return msg, nil
}

// extractBytes gets a value from a dict as []byte, handling both string and []byte types.
func extractBytes(dict map[string]interface{}, key string) ([]byte, bool) {
	val, ok := dict[key]
	if !ok {
		return nil, false
	}
	switch v := val.(type) {
	case string:
		return []byte(v), true
	case []byte:
		return v, true
	default:
		return nil, false
	}
}

// SendPEX sends a PEX message to the peer using their ut_pex extension message ID.
// Thread-safe: acquires the write mutex.
func (p *Peer) SendPEX(msg *PEXMessage) error {
	if p.UtPexID == 0 {
		return nil // peer doesn't support PEX
	}

	encoded, err := EncodePEXMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to encode PEX message: %w", err)
	}

	payload := make([]byte, 1+len(encoded))
	payload[0] = byte(p.UtPexID)
	copy(payload[1:], encoded)

	logger.Log.Debug("sending PEX", "peer", p.AddrPort, "added", len(msg.Added), "dropped", len(msg.Dropped))

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.sendOnly(internal.MessageExtension, payload)
}

// HandleExtension dispatches incoming extension sub-messages.
func (p *Peer) HandleExtension(payload []byte) {
	if len(payload) < 2 {
		return
	}
	subID := payload[0]
	if subID == internal.PEXLocalID && p.OnPEX != nil {
		msg, err := ParsePEXMessage(payload[1:])
		if err != nil {
			logger.Log.Debug("failed to parse PEX message", "peer", p.AddrPort, "error", err)
			return
		}
		logger.Log.Debug("received PEX", "peer", p.AddrPort, "added", len(msg.Added), "dropped", len(msg.Dropped))
		p.OnPEX(msg)
	}
}

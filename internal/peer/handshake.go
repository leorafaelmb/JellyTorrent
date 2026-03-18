package peer

import (
	"bytes"
	"fmt"
	"io"
	"net"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
	"github.com/leorafaelmb/JellyTorrent/internal/logger"
)

// Handshake represents the first message exchanged between peers.
type Handshake struct {
	PstrLen  byte
	Pstr     [19]byte
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

// constructHandshakeMessage creates the handshake message bytes.
func constructHandshakeMessage(infoHash [20]byte, ext bool) ([]byte, error) {
	message := make([]byte, internal.HandshakeLength)

	message[0] = internal.ProtocolStringLength
	copy(message[1:20], internal.ProtocolString)
	// leave 8 bytes blank for extension protocol
	copy(message[28:48], infoHash[:])
	copy(message[48:68], internal.PeerID)

	if ext {
		message[25] = internal.ExtensionID
	}

	return message, nil
}

func constructMagnetHandshakeMessage(infoHash [20]byte) []byte {
	message := make([]byte, internal.HandshakeLength)

	message[0] = byte(internal.ProtocolStringLength)
	copy(message[1:20], internal.ProtocolString)

	// Indicate extension support
	reserved := make([]byte, 8)
	reserved[internal.ExtensionBitPosition] = internal.ExtensionID
	copy(message[20:28], reserved)

	copy(message[28:48], infoHash[:])
	copy(message[48:68], internal.PeerID)

	return message
}

type ExtensionHandshakeResponse struct {
	MetadataSize     int
	UtMetadataID     int
	ExtensionMapping map[string]int
}

// Handshake performs the BitTorrent handshake with a peer.
func (p *Peer) Handshake(infoHash [20]byte, ext bool) (*Handshake, error) {
	logger.Log.Debug("performing handshake", "peer", p.AddrPort)

	c := p.Conn
	message, err := constructHandshakeMessage(infoHash, ext)
	if err != nil {
		return nil, fmt.Errorf("error constructing peer handshake message: %w", err)
	}
	_, err = c.Write(message)
	if err != nil {
		return nil, fmt.Errorf("error writing peer handshake message to connection: %w", err)
	}
	h, err := readHandshake(p.Conn)
	if err != nil {
		return nil, err
	}
	if infoHash != h.InfoHash {
		return h, fmt.Errorf("handshake info hash does not match torrent info hash: %w", err)

	}

	copy(p.ID[:], h.PeerID[:])

	logger.Log.Debug("handshake successful", "peer", p.AddrPort, "peerID", fmt.Sprintf("%x", h.PeerID))

	return h, nil
}

func (p *Peer) MagnetHandshake(infoHash [20]byte) (*Handshake, error) {
	logger.Log.Debug("performing magnet handshake", "peer", p.AddrPort)

	c := p.Conn
	message := constructMagnetHandshakeMessage(infoHash)

	_, err := c.Write(message)
	if err != nil {
		return nil, fmt.Errorf("error writing magnet handshake message: %w", err)
	}

	h, err := readHandshake(p.Conn)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(infoHash[:], h.InfoHash[:]) {
		return nil, fmt.Errorf("handshake info hash mismatch")
	}

	copy(p.ID[:], h.PeerID[:])

	// Check if peer supports extension protocol
	if h.Reserved[internal.ExtensionBitPosition]&internal.ExtensionID == 0 {
		return nil, fmt.Errorf("peer does not support extension protocol")
	}

	return h, nil
}

func (p *Peer) ExtensionHandshake() (*ExtensionHandshakeResponse, error) {
	logger.Log.Debug("extension handshake", "peer", p.AddrPort)

	payload := append([]byte{0}, []byte("d1:md11:ut_metadatai1eee")...)

	// Message ID 20 for extension protocol
	msg, err := p.SendMessage(20, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send extension handshake: %w", err)
	}

	if msg.ID != internal.MessageExtension {
		return nil, fmt.Errorf("expected extension message (20), got %d", msg.ID)
	}

	return parseExtensionHandshake(msg.Payload)
}

// readHandshake reads and parses a handshake message from the connection
func readHandshake(conn net.Conn) (*Handshake, error) {
	buf := make([]byte, 68)
	_, err := io.ReadFull(conn, buf)
	if err != nil {
		return nil, fmt.Errorf("error reading handshake response: %w", err)
	}

	h := &Handshake{}
	r := bytes.NewReader(buf)

	h.PstrLen, err = r.ReadByte()
	if err != nil {
		return nil, err
	}

	if _, err = io.ReadFull(r, h.Pstr[:]); err != nil {
		return nil, err
	}

	if _, err = io.ReadFull(r, h.Reserved[:]); err != nil {
		return nil, err
	}

	if _, err = io.ReadFull(r, h.InfoHash[:]); err != nil {
		return nil, err
	}

	if _, err = io.ReadFull(r, h.PeerID[:]); err != nil {
		return nil, err
	}

	// Validate handshake message
	if h.PstrLen != internal.ProtocolStringLength || string(h.Pstr[:]) != internal.ProtocolString {
		logger.Log.Debug("invalid protocol string", "pstr", string(h.Pstr[:]))
		err = fmt.Errorf("invalid handshake: %w", err)
	}
	return h, err
}

// ServerHandshake handles an incoming connection's handshake.
// Reads the remote peer's handshake first, validates the info hash, then responds with ours.
func ServerHandshake(conn net.Conn, infoHash [20]byte) (*Peer, error) {
	h, err := readHandshake(conn)
	if err != nil {
		return nil, fmt.Errorf("error reading incoming handshake: %w", err)
	}
	if h.InfoHash != infoHash {
		return nil, fmt.Errorf("info hash mismatch: expected %x, got %x", infoHash, h.InfoHash)
	}

	msg, err := constructHandshakeMessage(infoHash, false)
	if err != nil {
		return nil, fmt.Errorf("error constructing handshake response: %w", err)
	}
	if _, err := conn.Write(msg); err != nil {
		return nil, fmt.Errorf("error writing handshake response: %w", err)
	}

	p := &Peer{
		Conn:      conn,
		AmChoking: true, // per BT spec, start choked
	}
	copy(p.ID[:], h.PeerID[:])

	return p, nil
}

func parseExtensionHandshake(payload []byte) (*ExtensionHandshakeResponse, error) {
	decoded, err := bencode.Decode(payload[1:])
	if err != nil {
		return nil, fmt.Errorf("failed to decode extension handshake: %w", err)
	}

	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("extension handshake not a dictionary")
	}

	response := &ExtensionHandshakeResponse{
		ExtensionMapping: make(map[string]int),
	}

	if metadataSize, ok := dict["metadata_size"].(int); ok {
		response.MetadataSize = metadataSize
	}

	if m, ok := dict["m"].(map[string]interface{}); ok {
		for key, val := range m {
			if id, ok := val.(int); ok {
				response.ExtensionMapping[key] = id
				if key == "ut_metadata" {
					response.UtMetadataID = id
				}
			}
		}
	}

	if response.UtMetadataID == 0 {
		return nil, fmt.Errorf("peer does not support ut_metadata extension")
	}

	return response, nil
}

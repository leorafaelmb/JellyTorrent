package peer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/leorafaelmb/BitTorrent-Client/internal"
	"github.com/leorafaelmb/bencode"
	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/metainfo"
)

// BlockServer provides read access to completed piece data for serving upload requests.
type BlockServer interface {
	GetBlock(pieceIndex int, begin, length uint32) ([]byte, bool)
}

// Peer represents a network connection to another BitTorrent client.
type Peer struct {
	AddrPort netip.AddrPort
	ID       [20]byte

	Conn   net.Conn
	Choked bool

	BitField BitField

	// Upload state
	BlockServer BlockServer // nil means no upload support
	AmChoking   bool        // whether we are choking this peer (starts true per BT spec)
}

// PeerMessage represents a message sent between peers after the handshake
type PeerMessage struct {
	Length  uint32
	ID      byte
	Payload []byte
}

// IsKeepAlive returns true if this is a keep-alive message (length 0).
func (m *PeerMessage) IsKeepAlive() bool {
	return m.Length == 0
}

// Connect establishes a TCP connection to the peer
func (p *Peer) Connect() error {
	logger.Log.Debug("connecting to peer", "addr", p.AddrPort)
	conn, err := net.DialTimeout("tcp", p.AddrPort.String(), internal.ConnectionTimeout*time.Second)
	if err != nil {
		return fmt.Errorf("error connecting to peer: %w", err)
	}
	p.Conn = conn
	logger.Log.Debug("connected to peer", "addr", p.AddrPort)
	return nil
}

// SendMessage sends a message to the peer and waits for a response.
// Used for messages that expect an immediate reply.
func (p *Peer) SendMessage(messageID byte, payload []byte) (*PeerMessage, error) {
	length := uint32(len(payload) + 1)
	message := make([]byte, 4+length)

	binary.BigEndian.PutUint32(message[0:4], length)
	message[4] = messageID
	copy(message[5:], payload)

	if _, err := p.Conn.Write(message); err != nil {
		return nil, err
	}

	response, err := p.ReadMessage()

	return response, err
}

// ReadMessage reads one complete message from the peer.
// Blocks until a full message is received.
func (p *Peer) ReadMessage() (*PeerMessage, error) {
	lenBytes := make([]byte, 4)
	if _, err := io.ReadFull(p.Conn, lenBytes); err != nil {
		return nil, fmt.Errorf("error reading length of peer message: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBytes)

	// Keep-alive message (length 0)
	if length == 0 {
		return &PeerMessage{Length: 0}, nil
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(p.Conn, buf); err != nil {
		return nil, fmt.Errorf("error reading data stream into buffer: %w", err)
	}

	return &PeerMessage{
		Length:  length,
		ID:      buf[0],
		Payload: buf[1:],
	}, nil
}

// ReadBitfield reads and stores the peer's bitfield message.
func (p *Peer) ReadBitfield() (*PeerMessage, error) {
	msg, err := p.ReadMessage()
	if err != nil {
		return msg, fmt.Errorf("failed to read bitfield: %w", err)
	}
	if msg.ID != internal.MessageBitfield {
		return msg, fmt.Errorf("expected bitfield (5), got %d", msg.ID)
	}

	p.BitField = msg.Payload

	logger.Log.Debug("bitfield received", "bytes", len(msg.Payload))

	return msg, nil
}

// SendOnly sends a message without reading a response.
func (p *Peer) SendOnly(messageID byte, payload []byte) error {
	length := uint32(len(payload) + 1)
	message := make([]byte, 4+length)
	binary.BigEndian.PutUint32(message[0:4], length)
	message[4] = messageID
	copy(message[5:], payload)
	_, err := p.Conn.Write(message)
	return err
}

// SendInterested sends an interested message to the peer.
func (p *Peer) SendInterested() error {
	logger.Log.Debug("sending interested", "peer", p.AddrPort)
	return p.SendOnly(internal.MessageInterested, nil)
}

// SendNotInterested sends a not-interested message to the peer.
func (p *Peer) SendNotInterested() error {
	logger.Log.Debug("sending not-interested", "peer", p.AddrPort)
	return p.SendOnly(internal.MessageNotInterested, nil)
}

// HandleRequest processes an incoming Request message from a peer.
// Silently ignores if BlockServer is nil, we're choking the peer, or the piece is unavailable.
func (p *Peer) HandleRequest(payload []byte) error {
	if p.BlockServer == nil || p.AmChoking {
		return nil
	}
	if len(payload) < 12 {
		return nil
	}

	index := binary.BigEndian.Uint32(payload[0:4])
	begin := binary.BigEndian.Uint32(payload[4:8])
	length := binary.BigEndian.Uint32(payload[8:12])

	if length > 131072 {
		return nil
	}

	block, ok := p.BlockServer.GetBlock(int(index), begin, length)
	if !ok {
		return nil
	}

	piecePayload := make([]byte, 8+len(block))
	binary.BigEndian.PutUint32(piecePayload[0:4], index)
	binary.BigEndian.PutUint32(piecePayload[4:8], begin)
	copy(piecePayload[8:], block)

	logger.Log.Debug("serving block", "peer", p.AddrPort, "piece", index, "offset", begin, "length", length)
	return p.SendOnly(internal.MessagePiece, piecePayload)
}

// WaitForUnchoke reads messages until an Unchoke is received.
// Processes Have, KeepAlive, and other messages encountered along the way.
func (p *Peer) WaitForUnchoke() error {
	logger.Log.Debug("waiting for unchoke", "peer", p.AddrPort)
	for {
		msg, err := p.ReadMessage()
		if err != nil {
			return fmt.Errorf("error waiting for unchoke: %w", err)
		}

		if msg.IsKeepAlive() {
			continue
		}

		switch msg.ID {
		case internal.MessageUnchoke:
			p.Choked = false
			logger.Log.Debug("peer unchoked us", "peer", p.AddrPort)
			return nil
		case internal.MessageChoke:
			p.Choked = true
			logger.Log.Debug("peer choked us while waiting", "peer", p.AddrPort)
		case internal.MessageHave:
			if len(msg.Payload) >= 4 {
				idx := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
				p.BitField.SetPiece(idx)
				logger.Log.Debug("received have during unchoke wait", "peer", p.AddrPort, "piece", idx)
			}
		case internal.MessageBitfield:
			p.BitField = msg.Payload
		case internal.MessageRequest:
			if err := p.HandleRequest(msg.Payload); err != nil {
				logger.Log.Debug("error serving request during unchoke wait", "peer", p.AddrPort, "error", err)
			}
		}
	}
}

// SendRequest requests a specific block from a piece.
// index: which piece, begin: byte offset within piece, block: number of bytes
func (p *Peer) SendRequest(index, begin, block uint32) (*PeerMessage, error) {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], index)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	binary.BigEndian.PutUint32(payload[8:12], block)

	return p.SendMessage(6, payload)
}

// constructPieceRequest builds a request message
func (p *Peer) constructPieceRequest(index, begin, length uint32) []byte {
	request := make([]byte, 17)

	// Set message length
	binary.BigEndian.PutUint32(request[0:4], 13)

	// Set message ID
	request[4] = byte(6)

	// Set payload: index, begin, and length respectively
	binary.BigEndian.PutUint32(request[5:9], index)
	binary.BigEndian.PutUint32(request[9:13], begin)
	binary.BigEndian.PutUint32(request[13:17], length)

	return request

}

// BlockRequest represents a single block request within a piece
type BlockRequest struct {
	Index  uint32
	Begin  uint32
	Length uint32
}

// sendRequestOnly sends a request without waiting for a response.
// Used in pipelining to send multiple requests back-to-back.
func (p *Peer) sendRequestOnly(index, begin, length uint32) error {
	request := p.constructPieceRequest(index, begin, length)

	if _, err := p.Conn.Write(request); err != nil {
		return fmt.Errorf("error writing request to connection: %w", err)
	}

	return nil
}

// getBlocks downloads multiple blocks using TCP pipelining.
// Handles interleaved Choke, Have, KeepAlive, and Unchoke messages.
func (p *Peer) getBlocks(requests []BlockRequest) ([][]byte, error) {
	numBlocks := len(requests)
	blocks := make([][]byte, numBlocks)

	requested := 0
	received := 0

	for received < numBlocks {
		for requested < numBlocks && requested-received < internal.MaxPipelineRequests {
			req := requests[requested]

			if err := p.sendRequestOnly(req.Index, req.Begin, req.Length); err != nil {
				return nil, fmt.Errorf("error sending request for block %d: %w", requested, err)
			}
			requested++
		}

		msg, err := p.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("error reading message for block %d: %w", received, err)
		}

		if msg.IsKeepAlive() {
			continue
		}

		switch msg.ID {
		case internal.MessagePiece:
			if len(msg.Payload) < 8 {
				return nil, fmt.Errorf("piece message payload too short: %d bytes", len(msg.Payload))
			}
			blocks[received] = msg.Payload[8:]
			received++

		case internal.MessageChoke:
			p.Choked = true
			logger.Log.Warn("peer choked during block download", "peer", p.AddrPort)
			return nil, ErrChoked

		case internal.MessageUnchoke:
			p.Choked = false

		case internal.MessageHave:
			if len(msg.Payload) >= 4 {
				idx := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
				p.BitField.SetPiece(idx)
				logger.Log.Debug("received have during download", "peer", p.AddrPort, "piece", idx)
			}

		case internal.MessageRequest:
			if err := p.HandleRequest(msg.Payload); err != nil {
				logger.Log.Debug("error serving request during download", "peer", p.AddrPort, "error", err)
			}

		default:
			// Ignore unknown messages
		}
	}
	return blocks, nil
}

// GetPiece downloads and verifies a complete piece.
// Breaks the piece into 16KB blocks and uses pipelining for download efficiency.
func (p *Peer) GetPiece(pieceHash []byte, pieceLength, pieceIndex uint32) ([]byte, error) {
	piece := make([]byte, 0, pieceLength)

	var requests []BlockRequest
	var begin uint32 = 0
	remaining := pieceLength

	for remaining > 0 {
		blockLen := internal.BlockSize
		if remaining < internal.BlockSize {
			blockLen = remaining
		}

		requests = append(requests, BlockRequest{
			Index:  pieceIndex,
			Begin:  begin,
			Length: blockLen,
		})

		begin += blockLen
		remaining -= blockLen
	}

	logger.Log.Debug("downloading piece", "index", pieceIndex, "length", pieceLength, "blocks", len(requests))

	blocks, err := p.getBlocks(requests)
	if err != nil {
		return nil, fmt.Errorf("error downloading blocks: %w", err)
	}

	for _, block := range blocks {
		piece = append(piece, block...)
	}

	if !bytes.Equal(metainfo.HashPiece(piece), pieceHash) {
		logger.Log.Warn("piece hash mismatch", "index", pieceIndex)
		return nil, fmt.Errorf("invalid piece hash for piece %d", pieceIndex)
	}

	logger.Log.Debug("piece verified", "index", pieceIndex)

	return piece, nil
}

// RequestMetadataPiece requests a piece of the metadata
func (p *Peer) RequestMetadataPiece(utMetadataID byte, piece int) (*metainfo.MetadataPiece, error) {
	// Build request message
	request := fmt.Sprintf("d8:msg_typei0e5:piecei%dee", piece)

	payload := append([]byte{utMetadataID}, []byte(request)...)

	msg, err := p.SendMessage(20, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to send metadata request: %w", err)
	}

	if msg.ID != internal.MessageExtension {
		return nil, fmt.Errorf("expected extension message (20), got %d", msg.ID)
	}

	return metainfo.ParseMetadataPiece(msg.Payload)
}

func (p *Peer) DownloadMetadata(magnet *metainfo.MagnetLink) (*metainfo.Info, error) {
	// Perform extension handshake
	extResp, err := p.ExtensionHandshake()
	if err != nil {
		return nil, fmt.Errorf("extension handshake failed: %w", err)
	}

	if extResp.MetadataSize == 0 {
		return nil, fmt.Errorf("peer reported metadata_size of 0")
	}

	numPieces := (extResp.MetadataSize + internal.MetadataPieceSize - 1) / internal.MetadataPieceSize

	logger.Log.Info("downloading metadata", "bytes", extResp.MetadataSize, "pieces", numPieces)

	// Download metadata pieces
	metadata := make([]byte, 0, extResp.MetadataSize)
	for i := 0; i < numPieces; i++ {
		logger.Log.Debug("requesting metadata piece", "piece", i+1, "total", numPieces)

		piece, err := p.RequestMetadataPiece(byte(extResp.UtMetadataID), i)
		if err != nil {
			return nil, fmt.Errorf("failed to get metadata piece %d: %w", i, err)
		}

		metadata = append(metadata, piece.Data...)
	}

	// Trim to exact size
	if len(metadata) > extResp.MetadataSize {
		metadata = metadata[:extResp.MetadataSize]
	}

	// Verify info hash
	calculatedHash := metainfo.HashPiece(metadata)
	if !bytes.Equal(calculatedHash, magnet.InfoHash[:]) {
		return nil, fmt.Errorf("metadata hash mismatch")
	}

	logger.Log.Debug("metadata hash verified")

	// Decode metadata (it's a bencoded info dict)
	decoded, err := bencode.Decode(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	infoDict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata is not a dictionary")
	}

	return metainfo.NewInfo(infoDict)
}

func (p *Peer) ParseBitfield(msg *PeerMessage) error {
	if msg.ID != internal.MessageBitfield {
		return fmt.Errorf("expected bitfield message (id 5), got id %d", msg.ID)
	}
	p.BitField = msg.Payload
	return nil
}

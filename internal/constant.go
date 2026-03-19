package internal

import "time"

// Peer ID used for all BitTorrent communications
const PeerID = "leofeopeoluvsanayeli"

// BitTorrent Protocol
const (
	ProtocolString       = "BitTorrent protocol"
	ProtocolStringLength = 19
	HandshakeLength      = 68
)

// Message IDs
const (
	MessageChoke         byte = 0
	MessageUnchoke       byte = 1
	MessageInterested    byte = 2
	MessageNotInterested byte = 3
	MessageHave          byte = 4
	MessageBitfield      byte = 5
	MessageRequest       byte = 6
	MessagePiece         byte = 7
	MessageCancel        byte = 8
	MessageExtension     byte = 20 // BEP 10 Extension Protocol
)

// Download config
const (
	MaxPipelineRequests int    = 5       // Maximum concurrent block requests per peer
	BlockSize           uint32 = 1 << 14 // 16KB - standard block size
	MetadataPieceSize          = 1 << 14 // 16KB - metadata piece size for magnet links
)

// Network config
const (
	DefaultPort       = 6881
	DefaultUploaded   = 0
	DefaultDownloaded = 0
	DefaultCompact    = 1
	ConnectionTimeout = 3 // seconds
)

// Seeder config
const (
	DefaultMaxSeedPeers           = 50
	DefaultUnchokeSlots           = 4
	DefaultUnchokeInterval        = 30 * time.Second
	KeepAliveInterval             = 2 * time.Minute
	HandshakeTimeout              = 3 * time.Second
)

// HandshakeDeadline returns a time.Time suitable for net.Conn.SetDeadline during handshake.
func HandshakeDeadline() time.Time {
	return time.Now().Add(HandshakeTimeout)
}

// ZeroDeadline returns the zero time, which clears any connection deadline.
func ZeroDeadline() time.Time {
	return time.Time{}
}

// Magnet Link Extension
const (
	ExtensionBitPosition = 5 // Reserved byte index for extension bit
	ExtensionID          = 0x10
)

// PEX (Peer Exchange, BEP 11)
const (
	PEXInterval = 60 * time.Second // how often to send PEX messages
	PEXMaxAdded = 50               // max added peers per PEX message
	PEXLocalID  byte = 2           // our local ut_pex extension message ID
)

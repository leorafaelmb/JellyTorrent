package metainfo

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/leorafaelmb/JellyTorrent/internal/logger"
	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
)

type MagnetLink struct {
	TrackerURLs []string
	InfoHash    [20]byte
	HexInfoHash string
}

// TrackerURL returns the first tracker URL for convenience.
func (m *MagnetLink) TrackerURL() string {
	if len(m.TrackerURLs) > 0 {
		return m.TrackerURLs[0]
	}
	return ""
}

func DeserializeMagnet(uri string) (*MagnetLink, error) {
	logger.Log.Debug("deserializing magnet link")
	magnetUri, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	trackerURLs := magnetUri.Query()["tr"]
	hexInfoHash := strings.ReplaceAll(magnetUri.Query()["xt"][0], "urn:btih:", "")

	var infoHash [20]byte
	decodedHash, err := hex.DecodeString(hexInfoHash)
	if err != nil {
		return nil, err
	}
	copy(infoHash[:], decodedHash)

	logger.Log.Debug("magnet parsed", "trackers", len(trackerURLs), "infoHash", hexInfoHash)

	return &MagnetLink{
		TrackerURLs: trackerURLs,
		InfoHash:    infoHash,
		HexInfoHash: hexInfoHash,
	}, nil
}

type MetadataPiece struct {
	Piece     int
	TotalSize int
	Data      []byte
}

func ParseMetadataPiece(payload []byte) (*MetadataPiece, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("metadata response too short")
	}

	// First byte is extension message ID, skip
	bencodedPart := payload[1:]
	decoded, dictEnd, err := bencode.DecodeAt(bencodedPart, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to decode metadata response: %w", err)
	}
	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata response not a dictionary")
	}
	// Check msg_type (should be 1 for data)
	msgType, ok := dict["msg_type"].(int)
	if !ok || msgType != 1 {
		return nil, fmt.Errorf("invalid msg_type in metadata response")
	}

	piece, ok := dict["piece"].(int)
	if !ok {
		return nil, fmt.Errorf("no piece index in metadata response")
	}

	totalSize, ok := dict["total_size"].(int)
	if !ok {
		return nil, fmt.Errorf("no total_size in metadata response")
	}

	// Extract the actual metadata data (everything after the bencoded dict)
	data := bencodedPart[dictEnd:]
	return &MetadataPiece{
		Piece:     piece,
		TotalSize: totalSize,
		Data:      data,
	}, nil
}

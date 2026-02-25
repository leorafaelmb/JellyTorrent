package metainfo

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/leorafaelmb/BitTorrent-Client/internal/bencode"
	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
)

// TorrentFile represents a parsed .torrent file
type TorrentFile struct {
	Announce     string // the "preferred" announce link provided by the torrent
	AnnounceList []string
	Info         *Info
}

// newTorrentFile constructs a TorrentFile given a decoded dictionary of a torrent file's contents
func newTorrentFile(dict interface{}) (*TorrentFile, error) {
	d, ok := dict.(map[string]interface{})
	var announceList []string

	if !ok {
		return nil, fmt.Errorf("newTorrent: argument is not a map")
	}
	announce, ok := d["announce"].(string)
	if !ok {
		return nil, fmt.Errorf("newTorrent: announce is not a string")
	}

	aList, ok := d["announce-list"].([]interface{})
	if !ok {
		announceList = nil
	} else {
		announceList = make([]string, len(aList))
		for i, item := range aList {
			// why on earth is there a list for each URL, with only that URL, already inside a list? [[url] [url]]wtf?
			dumbList, ok := item.([]interface{})
			if !ok {
				continue
			}
			link, ok := dumbList[0].(string)
			if !ok {
				continue
			}
			announceList[i] = link
		}
	}
	logger.Log.Debug("parsed announce list", "trackers", announceList)
	infoMap, ok := d["info"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("newTorrent: info value is not a map")
	}
	info, err := NewInfo(infoMap)
	if err != nil {
		return nil, fmt.Errorf("error creating Info struct: %w", err)
	}

	info.InfoHash = info.getInfoHash()
	logger.Log.Debug("torrent parsed", "announce", announce, "name", info.Name)
	return &TorrentFile{
		Announce:     announce,
		AnnounceList: announceList,
		Info:         info,
	}, nil
}

// DeserializeTorrent reads and parses a .torrent file from disk.
func DeserializeTorrent(filePath string) (*TorrentFile, error) {
	logger.Log.Debug("deserializing torrent", "path", filePath)
	contents, err := parseTorrent(filePath)
	if err != nil {
		return nil, fmt.Errorf("error parsing torrent file: %w", err)
	}
	decoded, err := bencode.Decode(contents)
	if err != nil {
		return nil, fmt.Errorf("error decoding torrent file path contents: %w", err)
	}

	return newTorrentFile(decoded)
}

// parseTorrent reads a .torrent file and returns its raw contents
func parseTorrent(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening torrent file: %w", err)
	}
	defer f.Close()
	fileInfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("error reading file info: %w", err)
	}

	fileSize := fileInfo.Size()

	fileBytes := make([]byte, fileSize)

	_, err = io.ReadFull(f, fileBytes)
	if err != nil {
		return nil, fmt.Errorf("error reading file into byte slice: %w", err)
	}

	return fileBytes, nil
}

// String returns a string representation of the torrent file
func (t TorrentFile) String() string {
	filesInfo := ""
	if t.Info.IsSingleFile() {
		filesInfo = fmt.Sprintf("Single File: %s (%d bytes)", t.Info.Name, t.Info.Length)
	} else {
		filesInfo = fmt.Sprintf("Multi-File: %s (root directory)\n", t.Info.Name)
		for i, f := range t.Info.Files {
			path := strings.Join(f.Path, "/")
			filesInfo += fmt.Sprintf("  File %d: %s (%d bytes)\n", i+1, path, f.Length)
		}
	}

	return fmt.Sprintf(
		"Tracker URL: %s\nLength: %d\nInfo Hash: %x\nPiece Length: %d\n%s\nPiece Hashes:\n%s",
		t.Announce, t.Info.Length, t.Info.getInfoHash(), t.Info.PieceLength,
		strings.TrimSpace(filesInfo),
		t.Info.GetPieceHashesStr(),
	)
}

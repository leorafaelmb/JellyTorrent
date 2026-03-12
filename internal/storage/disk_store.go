package storage

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DiskStore persists completed pieces to disk, enabling download resume
// and memory-efficient seeding.
type DiskStore struct {
	mu        sync.Mutex
	dir       string
	numPieces int
	pieceLen  int
	totalLen  int
	bitfield  []bool
}

// NewDiskStore creates a DiskStore for the given torrent. It creates the
// storage directory if needed and loads any existing bitfield for resume.
func NewDiskStore(baseDir string, infoHash [20]byte, numPieces, pieceLen, totalLen int) (*DiskStore, error) {
	dir := filepath.Join(baseDir, hex.EncodeToString(infoHash[:]))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("storage: create dir: %w", err)
	}

	ds := &DiskStore{
		dir:       dir,
		numPieces: numPieces,
		pieceLen:  pieceLen,
		totalLen:  totalLen,
		bitfield:  make([]bool, numPieces),
	}

	// Load existing bitfield if present
	ds.loadBitfield()

	return ds, nil
}

// WritePiece writes piece data to disk and updates the bitfield file.
func (ds *DiskStore) WritePiece(index int, data []byte) error {
	if index < 0 || index >= ds.numPieces {
		return fmt.Errorf("storage: piece index %d out of range [0, %d)", index, ds.numPieces)
	}

	path := ds.piecePath(index)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("storage: write piece %d: %w", index, err)
	}

	ds.mu.Lock()
	ds.bitfield[index] = true
	ds.mu.Unlock()

	return ds.saveBitfield()
}

// ReadPiece reads piece data from disk.
func (ds *DiskStore) ReadPiece(index int) ([]byte, error) {
	if index < 0 || index >= ds.numPieces {
		return nil, fmt.Errorf("storage: piece index %d out of range [0, %d)", index, ds.numPieces)
	}

	ds.mu.Lock()
	has := ds.bitfield[index]
	ds.mu.Unlock()

	if !has {
		return nil, fmt.Errorf("storage: piece %d not available", index)
	}

	data, err := os.ReadFile(ds.piecePath(index))
	if err != nil {
		return nil, fmt.Errorf("storage: read piece %d: %w", index, err)
	}
	return data, nil
}

// HasPiece returns true if the piece has been written to disk.
func (ds *DiskStore) HasPiece(index int) bool {
	if index < 0 || index >= ds.numPieces {
		return false
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.bitfield[index]
}

// CompletedBitfield returns a copy of which pieces are present on disk.
func (ds *DiskStore) CompletedBitfield() []bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	result := make([]bool, ds.numPieces)
	copy(result, ds.bitfield)
	return result
}

// Close is a no-op for now; all writes are already synced.
func (ds *DiskStore) Close() error {
	return nil
}

func (ds *DiskStore) piecePath(index int) string {
	return filepath.Join(ds.dir, fmt.Sprintf("piece-%06d", index))
}

func (ds *DiskStore) bitfieldPath() string {
	return filepath.Join(ds.dir, "bitfield")
}

// saveBitfield writes the bitfield to disk. Each byte represents one piece
// (0x00 = missing, 0x01 = present). Simple and easy to inspect.
func (ds *DiskStore) saveBitfield() error {
	ds.mu.Lock()
	buf := make([]byte, ds.numPieces)
	for i, has := range ds.bitfield {
		if has {
			buf[i] = 1
		}
	}
	ds.mu.Unlock()

	path := ds.bitfieldPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return fmt.Errorf("storage: write bitfield: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("storage: rename bitfield: %w", err)
	}
	return nil
}

// loadBitfield reads a previously saved bitfield from disk.
func (ds *DiskStore) loadBitfield() {
	data, err := os.ReadFile(ds.bitfieldPath())
	if err != nil {
		return // no bitfield file — fresh start
	}

	n := len(data)
	if n > ds.numPieces {
		n = ds.numPieces
	}
	for i := 0; i < n; i++ {
		ds.bitfield[i] = data[i] == 1
	}
}

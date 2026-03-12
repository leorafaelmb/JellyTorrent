package storage

import (
	"bytes"
	"os"
	"testing"
)

func newTestStore(t *testing.T, numPieces, pieceLen, totalLen int) *DiskStore {
	t.Helper()
	dir := t.TempDir()
	hash := [20]byte{0x01, 0x02, 0x03}
	ds, err := NewDiskStore(dir, hash, numPieces, pieceLen, totalLen)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	return ds
}

func TestWriteReadPiece(t *testing.T) {
	ds := newTestStore(t, 4, 100, 400)

	data := []byte("hello piece 2")
	if err := ds.WritePiece(2, data); err != nil {
		t.Fatalf("WritePiece: %v", err)
	}

	got, err := ds.ReadPiece(2)
	if err != nil {
		t.Fatalf("ReadPiece: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("expected %q, got %q", data, got)
	}
}

func TestHasPiece(t *testing.T) {
	ds := newTestStore(t, 3, 100, 300)

	if ds.HasPiece(0) {
		t.Fatal("expected HasPiece(0) = false before write")
	}

	ds.WritePiece(0, []byte("data"))

	if !ds.HasPiece(0) {
		t.Fatal("expected HasPiece(0) = true after write")
	}
	if ds.HasPiece(1) {
		t.Fatal("expected HasPiece(1) = false")
	}
}

func TestHasPieceOutOfRange(t *testing.T) {
	ds := newTestStore(t, 2, 100, 200)

	if ds.HasPiece(-1) {
		t.Fatal("expected false for negative index")
	}
	if ds.HasPiece(5) {
		t.Fatal("expected false for out-of-range index")
	}
}

func TestCompletedBitfield(t *testing.T) {
	ds := newTestStore(t, 5, 100, 500)

	ds.WritePiece(1, []byte("p1"))
	ds.WritePiece(3, []byte("p3"))

	bf := ds.CompletedBitfield()
	expected := []bool{false, true, false, true, false}
	for i, want := range expected {
		if bf[i] != want {
			t.Errorf("bitfield[%d] = %t, want %t", i, bf[i], want)
		}
	}

	// Verify it's a copy
	bf[0] = true
	if ds.HasPiece(0) {
		t.Fatal("CompletedBitfield should return a copy")
	}
}

func TestResumeBitfield(t *testing.T) {
	dir := t.TempDir()
	hash := [20]byte{0xAA, 0xBB}

	// First session: write some pieces
	ds1, err := NewDiskStore(dir, hash, 4, 100, 400)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ds1.WritePiece(0, []byte("piece0"))
	ds1.WritePiece(2, []byte("piece2"))
	ds1.Close()

	// Second session: should resume with existing bitfield
	ds2, err := NewDiskStore(dir, hash, 4, 100, 400)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}

	if !ds2.HasPiece(0) {
		t.Fatal("expected piece 0 to be present after resume")
	}
	if ds2.HasPiece(1) {
		t.Fatal("expected piece 1 to be absent after resume")
	}
	if !ds2.HasPiece(2) {
		t.Fatal("expected piece 2 to be present after resume")
	}

	// Verify data is readable
	data, err := ds2.ReadPiece(0)
	if err != nil {
		t.Fatalf("ReadPiece: %v", err)
	}
	if !bytes.Equal(data, []byte("piece0")) {
		t.Fatalf("expected %q, got %q", "piece0", data)
	}
}

func TestReadPieceNotAvailable(t *testing.T) {
	ds := newTestStore(t, 2, 100, 200)

	_, err := ds.ReadPiece(0)
	if err == nil {
		t.Fatal("expected error reading unavailable piece")
	}
}

func TestWritePieceOutOfRange(t *testing.T) {
	ds := newTestStore(t, 2, 100, 200)

	if err := ds.WritePiece(-1, []byte("data")); err == nil {
		t.Fatal("expected error for negative index")
	}
	if err := ds.WritePiece(5, []byte("data")); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestBitfieldAtomicWrite(t *testing.T) {
	ds := newTestStore(t, 3, 100, 300)

	ds.WritePiece(0, []byte("p0"))
	ds.WritePiece(1, []byte("p1"))

	// Verify no .tmp file is left behind
	entries, _ := os.ReadDir(ds.dir)
	for _, e := range entries {
		if e.Name() == "bitfield.tmp" {
			t.Fatal("temporary bitfield file should not persist")
		}
	}
}

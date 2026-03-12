package dht

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	a := newTestDHT(t)
	defer a.Close()
	b := newTestDHT(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.config.BootstrapNodes = []string{localAddr(a)}
	b.Bootstrap(ctx)
	time.Sleep(100 * time.Millisecond)

	if b.table.NumNodes() == 0 {
		t.Fatal("B should have nodes after bootstrap")
	}

	// Save B's routing table.
	tmp := filepath.Join(t.TempDir(), "routing_table.dat")
	if err := b.Save(tmp); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	originalID := b.id
	originalNodes := b.table.NumNodes()
	b.Close()

	// Create a new DHT loading the saved state.
	c, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithRoutingTable(tmp),
	)
	if err != nil {
		t.Fatalf("New with saved state failed: %v", err)
	}
	defer c.Close()

	if c.id != originalID {
		t.Errorf("node ID not preserved: got %x, want %x", c.id, originalID)
	}
	if c.table.NumNodes() != originalNodes {
		t.Errorf("node count mismatch: got %d, want %d", c.table.NumNodes(), originalNodes)
	}
}

func TestSaveLoadMissingFile(t *testing.T) {
	d, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithRoutingTable(filepath.Join(t.TempDir(), "nonexistent.dat")),
	)
	if err != nil {
		t.Fatalf("New should succeed with missing state file: %v", err)
	}
	defer d.Close()
}

func TestSaveLoadCorruptFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "corrupt.dat")
	os.WriteFile(tmp, []byte("not valid bencode!!!"), 0644)

	d, err := New(
		WithPort(0),
		WithBootstrapNodes(nil),
		WithRoutingTable(tmp),
	)
	if err != nil {
		t.Fatalf("New should succeed with corrupt state file: %v", err)
	}
	defer d.Close()
}

func TestSaveAtomicity(t *testing.T) {
	d := newTestDHT(t)
	defer d.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.dat")

	if err := d.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Final file should exist.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file should exist: %v", err)
	}

	// Temp file should not exist.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should be cleaned up after save")
	}
}

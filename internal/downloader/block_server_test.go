package downloader

import (
	"testing"

	"github.com/leorafaelmb/BitTorrent-Client/internal/peer"
)

func TestGetPieceData_Completed(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 0, nil)

	pm.Complete(0, []byte("piece-zero-data"))

	data, ok := pm.GetPieceData(0)
	if !ok || string(data) != "piece-zero-data" {
		t.Fatalf("expected completed piece data, got ok=%t data=%q", ok, data)
	}
}

func TestGetPieceData_NotCompleted(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 0, nil)

	_, ok := pm.GetPieceData(0)
	if ok {
		t.Fatal("expected ok=false for pending piece")
	}
}

func TestGetPieceData_OutOfRange(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 0, nil)

	_, ok := pm.GetPieceData(-1)
	if ok {
		t.Fatal("expected ok=false for negative index")
	}

	_, ok = pm.GetPieceData(5)
	if ok {
		t.Fatal("expected ok=false for out-of-range index")
	}
}

func TestBlockServer_GetBlock(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 16},
	}, &SequentialSelector{}, 0, nil)

	pm.Complete(0, []byte("0123456789abcdef"))

	bs := NewBlockServer(pm)

	// Full piece
	block, ok := bs.GetBlock(0, 0, 16)
	if !ok || string(block) != "0123456789abcdef" {
		t.Fatalf("expected full piece, got ok=%t block=%q", ok, block)
	}

	// Partial block
	block, ok = bs.GetBlock(0, 4, 4)
	if !ok || string(block) != "4567" {
		t.Fatalf("expected partial block, got ok=%t block=%q", ok, block)
	}

	// Out of bounds
	_, ok = bs.GetBlock(0, 10, 10)
	if ok {
		t.Fatal("expected ok=false for out-of-bounds block")
	}

	// Piece not available
	_, ok = bs.GetBlock(1, 0, 4)
	if ok {
		t.Fatal("expected ok=false for unavailable piece")
	}
}

func TestProgress(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
		{Index: 2, Hash: nil, Length: 50},
	}, &SequentialSelector{}, 0, nil)

	completed, total := pm.Progress()
	if completed != 0 || total != 3 {
		t.Fatalf("expected (0, 3), got (%d, %d)", completed, total)
	}

	pm.Complete(0, []byte("data0"))
	pm.Complete(2, []byte("data2"))

	completed, total = pm.Progress()
	if completed != 2 || total != 3 {
		t.Fatalf("expected (2, 3), got (%d, %d)", completed, total)
	}
}

func TestEndgameAssignment(t *testing.T) {
	// 3 pieces, endgame threshold of 2
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
		{Index: 2, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 2, nil)

	// Bitfield: peer has all 3 pieces
	bf := make(peer.BitField, 1)
	bf.SetPiece(0)
	bf.SetPiece(1)
	bf.SetPiece(2)

	// Normal mode: assign piece 0 to peerA
	info, ok := pm.Assign("peerA", bf)
	if !ok || info.Index != 0 {
		t.Fatalf("expected piece 0, got %v ok=%t", info, ok)
	}

	// Assign piece 1 to peerB
	info, ok = pm.Assign("peerB", bf)
	if !ok || info.Index != 1 {
		t.Fatalf("expected piece 1, got %v ok=%t", info, ok)
	}

	// Complete piece 0 — now 2 remain (1 pending + 1 in-progress), triggers endgame
	pm.Complete(0, []byte("data0"))

	// Assign to peerC — should get piece 2 (pending, preferred over in-progress)
	info, ok = pm.Assign("peerC", bf)
	if !ok || info.Index != 2 {
		t.Fatalf("expected piece 2 (pending), got %v ok=%t", info, ok)
	}

	// Now both remaining pieces are InProgress (1 by peerB, 2 by peerC)
	// peerA should get a duplicate endgame assignment
	info, ok = pm.Assign("peerA", bf)
	if !ok {
		t.Fatal("expected endgame assignment, got ok=false")
	}
	if info.Index != 1 && info.Index != 2 {
		t.Fatalf("expected piece 1 or 2 (endgame), got %d", info.Index)
	}

	// peerB should NOT get piece 1 again (already owns it), should get piece 2
	info, ok = pm.Assign("peerB", bf)
	if !ok || info.Index != 2 {
		t.Fatalf("expected piece 2 (endgame, not own piece), got %v ok=%t", info, ok)
	}
}

func TestEndgameNotActiveAboveThreshold(t *testing.T) {
	// 5 pieces, endgame threshold of 2
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
		{Index: 2, Hash: nil, Length: 100},
		{Index: 3, Hash: nil, Length: 100},
		{Index: 4, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 2, nil)

	bf := make(peer.BitField, 1)
	for i := 0; i < 5; i++ {
		bf.SetPiece(i)
	}

	// Assign all 5 pieces
	for i := 0; i < 5; i++ {
		_, ok := pm.Assign("peer"+string(rune('A'+i)), bf)
		if !ok {
			t.Fatalf("expected assignment for piece %d", i)
		}
	}

	// All pieces are InProgress, 5 remaining > threshold 2 — no endgame
	_, ok := pm.Assign("peerX", bf)
	if ok {
		t.Fatal("expected no assignment (endgame should not be active)")
	}
}

func TestEndgameCompleteIdempotent(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 1, nil)

	// Complete the same piece twice — should not panic or double-count
	pm.Complete(0, []byte("data"))
	pm.Complete(0, []byte("other-data"))

	data, ok := pm.GetPieceData(0)
	if !ok || string(data) != "data" {
		t.Fatalf("expected original data preserved, got ok=%t data=%q", ok, data)
	}
}

func TestHaveSubscription(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
	}, &SequentialSelector{}, 0, nil)

	ch := pm.SubscribeHave()

	pm.Complete(0, []byte("data0"))

	select {
	case idx := <-ch:
		if idx != 0 {
			t.Fatalf("expected piece index 0, got %d", idx)
		}
	default:
		t.Fatal("expected notification on Have channel")
	}

	pm.UnsubscribeHave(ch)

	pm.Complete(1, []byte("data1"))

	select {
	case <-ch:
		t.Fatal("should not receive notification after unsubscribe")
	default:
		// expected
	}
}

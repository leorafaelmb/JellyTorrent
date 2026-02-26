package downloader

import "testing"

func TestGetPieceData_Completed(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
	}, &SequentialSelector{})

	pm.Complete(0, []byte("piece-zero-data"))

	data, ok := pm.GetPieceData(0)
	if !ok || string(data) != "piece-zero-data" {
		t.Fatalf("expected completed piece data, got ok=%t data=%q", ok, data)
	}
}

func TestGetPieceData_NotCompleted(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
	}, &SequentialSelector{})

	_, ok := pm.GetPieceData(0)
	if ok {
		t.Fatal("expected ok=false for pending piece")
	}
}

func TestGetPieceData_OutOfRange(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
	}, &SequentialSelector{})

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
	}, &SequentialSelector{})

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
	}, &SequentialSelector{})

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

func TestHaveSubscription(t *testing.T) {
	pm := NewPieceManager([]PieceInfo{
		{Index: 0, Hash: nil, Length: 100},
		{Index: 1, Hash: nil, Length: 100},
	}, &SequentialSelector{})

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

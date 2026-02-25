package downloader

import "testing"

func TestRarestFirstSelector_EmptyCandidates(t *testing.T) {
	s := &RarestFirstSelector{}
	_, ok := s.Select(nil, nil)
	if ok {
		t.Fatal("expected ok=false for empty candidates")
	}
}

func TestRarestFirstSelector_SingleCandidate(t *testing.T) {
	s := &RarestFirstSelector{}
	availability := []int{5, 3, 7, 1, 4}
	idx, ok := s.Select([]int{2}, availability)
	if !ok || idx != 2 {
		t.Fatalf("expected (2, true), got (%d, %t)", idx, ok)
	}
}

func TestRarestFirstSelector_PicksRarest(t *testing.T) {
	s := &RarestFirstSelector{}
	availability := []int{5, 3, 7, 1, 4}
	// candidates: pieces 0,1,2,3 — piece 3 has availability 1 (rarest)
	idx, ok := s.Select([]int{0, 1, 2, 3}, availability)
	if !ok || idx != 3 {
		t.Fatalf("expected (3, true), got (%d, %t)", idx, ok)
	}
}

func TestRarestFirstSelector_TieBreaking(t *testing.T) {
	s := &RarestFirstSelector{}
	availability := []int{2, 2, 5, 2}
	// pieces 0,1,3 all have availability 2 — result should be one of them
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		idx, ok := s.Select([]int{0, 1, 3}, availability)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if idx != 0 && idx != 1 && idx != 3 {
			t.Fatalf("unexpected index %d", idx)
		}
		seen[idx] = true
	}
	// With 100 iterations and 3 options, we should see at least 2 different values
	if len(seen) < 2 {
		t.Fatal("expected random tie-breaking to produce varied results")
	}
}

func TestSequentialSelector_PicksLowestIndex(t *testing.T) {
	s := &SequentialSelector{}
	idx, ok := s.Select([]int{5, 2, 8, 1, 3}, nil)
	if !ok || idx != 1 {
		t.Fatalf("expected (1, true), got (%d, %t)", idx, ok)
	}
}

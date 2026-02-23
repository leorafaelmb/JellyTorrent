package downloader

// PieceSelector decides which piece index to pick from a set of candidates.
// candidates contains indices of pieces that are Pending and that the peer has.
// availability contains per-piece counts of how many connected peers have each piece.
type PieceSelector interface {
	Select(candidates []int, availability []int) (int, bool)
}

// SequentialSelector picks the lowest-index candidate.
type SequentialSelector struct{}

func (s *SequentialSelector) Select(candidates []int, _ []int) (int, bool) {
	if len(candidates) == 0 {
		return -1, false
	}
	min := candidates[0]
	for _, c := range candidates[1:] {
		if c < min {
			min = c
		}
	}
	return min, true
}

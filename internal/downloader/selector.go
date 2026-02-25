package downloader

import "math/rand"

// PieceSelector decides which piece index to pick from a set of candidates.
// candidates contains indices of pieces that are Pending and that the peer has.
// availability contains per-piece counts of how many connected peers have each piece.
type PieceSelector interface {
	Select(candidates []int, availability []int) (int, bool)
}

// RarestFirstSelector picks the candidate with the fewest available peers.
// Ties are broken randomly to avoid all peers requesting the same rare piece.
type RarestFirstSelector struct{}

func (s *RarestFirstSelector) Select(candidates []int, availability []int) (int, bool) {
	if len(candidates) == 0 {
		return -1, false
	}

	// Collect candidates with the lowest availability
	minAvail := availability[candidates[0]]
	rarest := []int{candidates[0]}

	for _, c := range candidates[1:] {
		a := availability[c]
		if a < minAvail {
			minAvail = a
			rarest = rarest[:1]
			rarest[0] = c
		} else if a == minAvail {
			rarest = append(rarest, c)
		}
	}

	return rarest[rand.Intn(len(rarest))], true
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

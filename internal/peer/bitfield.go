package peer

// BitField is a compact representation of which pieces a peer has.
type BitField []byte

// NewBitField creates a zeroed bitfield for the given number of pieces.
func NewBitField(numPieces int) BitField {
	return make(BitField, (numPieces+7)/8)
}

// HasPiece checks if the bit at index is set.
func (bf BitField) HasPiece(index int) bool {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}
	return bf[byteIndex]>>(7-offset)&1 != 0
}

// SetPiece sets the bit at index, marking a piece as available.
func (bf BitField) SetPiece(index int) {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex >= 0 && byteIndex < len(bf) {
		bf[byteIndex] |= 1 << (7 - offset)
	}
}

package metainfo

import (
	"crypto/sha1"
)

// HashPiece computes the SHA1 hash of a piece for verification
func HashPiece(piece []byte) []byte {
	hasher := sha1.New()
	hasher.Write(piece)
	sha := hasher.Sum(nil)
	return sha
}

package peer

import "errors"

var ErrChoked = errors.New("peer choked us")
var ErrPieceCancelled = errors.New("piece completed by another peer")

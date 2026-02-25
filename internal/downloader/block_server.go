package downloader

import "github.com/leorafaelmb/BitTorrent-Client/internal/peer"

// PieceManagerBlockServer adapts PieceManager to the peer.BlockServer interface.
type PieceManagerBlockServer struct {
	pm *PieceManager
}

// Compile-time check that PieceManagerBlockServer implements peer.BlockServer.
var _ peer.BlockServer = (*PieceManagerBlockServer)(nil)

// NewBlockServer creates a BlockServer backed by a PieceManager.
func NewBlockServer(pm *PieceManager) *PieceManagerBlockServer {
	return &PieceManagerBlockServer{pm: pm}
}

func (bs *PieceManagerBlockServer) GetBlock(pieceIndex int, begin, length uint32) ([]byte, bool) {
	data, ok := bs.pm.GetPieceData(pieceIndex)
	if !ok {
		return nil, false
	}
	end := int(begin + length)
	if int(begin) >= len(data) || end > len(data) {
		return nil, false
	}
	return data[begin:end], true
}

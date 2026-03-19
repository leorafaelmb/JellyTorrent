package seeder

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal"
	"github.com/leorafaelmb/JellyTorrent/internal/peer"
)

// mockBlockServer is a test BlockServer that returns predictable data.
type mockBlockServer struct {
	pieces map[int][]byte
}

func (m *mockBlockServer) GetBlock(pieceIndex int, begin, length uint32) ([]byte, bool) {
	data, ok := m.pieces[pieceIndex]
	if !ok {
		return nil, false
	}
	end := int(begin + length)
	if int(begin) >= len(data) || end > len(data) {
		return nil, false
	}
	return data[begin:end], true
}

func newTestBlockServer() *mockBlockServer {
	bs := &mockBlockServer{pieces: make(map[int][]byte)}
	// Create 3 pieces of 32KB each
	for i := 0; i < 3; i++ {
		data := make([]byte, 32*1024)
		for j := range data {
			data[j] = byte(i)
		}
		bs.pieces[i] = data
	}
	return bs
}

// writeHandshake writes a BT handshake to conn.
func writeHandshake(conn net.Conn, infoHash [20]byte) error {
	msg := make([]byte, 68)
	msg[0] = 19
	copy(msg[1:20], "BitTorrent protocol")
	copy(msg[28:48], infoHash[:])
	copy(msg[48:68], "test-peer-id-1234567")
	_, err := conn.Write(msg)
	return err
}

// readHandshakeResponse reads and validates a handshake response.
func readHandshakeResponse(conn net.Conn, expectedInfoHash [20]byte) error {
	buf := make([]byte, 68)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("read handshake: %w", err)
	}
	if buf[0] != 19 || string(buf[1:20]) != "BitTorrent protocol" {
		return fmt.Errorf("invalid protocol string")
	}
	var gotHash [20]byte
	copy(gotHash[:], buf[28:48])
	if gotHash != expectedInfoHash {
		return fmt.Errorf("info hash mismatch")
	}
	return nil
}

// readMessage reads a BT message from conn.
func readMessage(conn net.Conn) (byte, []byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf)
	if length == 0 {
		return 0, nil, nil // keep-alive
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

// writeMessage writes a BT message to conn.
func writeMessage(conn net.Conn, id byte, payload []byte) error {
	length := uint32(len(payload) + 1)
	msg := make([]byte, 4+length)
	binary.BigEndian.PutUint32(msg[0:4], length)
	msg[4] = id
	copy(msg[5:], payload)
	_, err := conn.Write(msg)
	return err
}

func TestServerHandshake(t *testing.T) {
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		p, _, err := peer.ServerHandshake(server, infoHash)
		if err != nil {
			done <- err
			return
		}
		if string(p.ID[:]) != "test-peer-id-1234567" {
			done <- fmt.Errorf("unexpected peer ID: %x", p.ID)
			return
		}
		done <- nil
	}()

	// Client sends handshake
	if err := writeHandshake(client, infoHash); err != nil {
		t.Fatal(err)
	}
	// Client reads response
	if err := readHandshakeResponse(client, infoHash); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServerHandshakeWrongHash(t *testing.T) {
	infoHash := [20]byte{1, 2, 3}
	wrongHash := [20]byte{4, 5, 6}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, _, err := peer.ServerHandshake(server, infoHash)
		done <- err
	}()

	if err := writeHandshake(client, wrongHash); err != nil {
		t.Fatal(err)
	}

	err := <-done
	if err == nil {
		t.Fatal("expected error for wrong info hash")
	}
}

func TestSeederAcceptAndServePiece(t *testing.T) {
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	bs := newTestBlockServer()

	bitfield := peer.NewBitField(3)
	bitfield.SetPiece(0)
	bitfield.SetPiece(1)
	bitfield.SetPiece(2)

	s := New(infoHash, 3, bs, bitfield, nil,
		WithListenPort(0), // random port
		WithUnchokeSlots(10),
		WithUnchokeInterval(200*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seederDone := make(chan error, 1)
	go func() {
		seederDone <- s.Run(ctx)
	}()

	// Wait for listener
	time.Sleep(50 * time.Millisecond)

	port := s.ListenPort()
	if port == 0 {
		t.Fatal("seeder did not bind to a port")
	}

	// Connect as client
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send handshake
	if err := writeHandshake(conn, infoHash); err != nil {
		t.Fatal(err)
	}

	// Read handshake response
	if err := readHandshakeResponse(conn, infoHash); err != nil {
		t.Fatal(err)
	}

	// Read bitfield message
	id, payload, err := readMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if id != internal.MessageBitfield {
		t.Fatalf("expected bitfield (5), got %d", id)
	}
	bf := peer.BitField(payload)
	if !bf.HasPiece(0) || !bf.HasPiece(1) || !bf.HasPiece(2) {
		t.Fatal("bitfield missing pieces")
	}

	// Send Interested
	if err := writeMessage(conn, internal.MessageInterested, nil); err != nil {
		t.Fatal(err)
	}

	// Wait for choker to unchoke us
	time.Sleep(100 * time.Millisecond)

	// Read Unchoke
	unchoked := false
	for i := 0; i < 5; i++ {
		id, _, err = readMessage(conn)
		if err != nil {
			t.Fatal(err)
		}
		if id == internal.MessageUnchoke {
			unchoked = true
			break
		}
	}
	if !unchoked {
		t.Fatal("never received unchoke")
	}

	// Request a block: piece 0, offset 0, length 16KB
	reqPayload := make([]byte, 12)
	binary.BigEndian.PutUint32(reqPayload[0:4], 0)    // piece index
	binary.BigEndian.PutUint32(reqPayload[4:8], 0)    // begin
	binary.BigEndian.PutUint32(reqPayload[8:12], 16384) // length
	if err := writeMessage(conn, internal.MessageRequest, reqPayload); err != nil {
		t.Fatal(err)
	}

	// Read Piece response
	id, payload, err = readMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if id != internal.MessagePiece {
		t.Fatalf("expected piece (7), got %d", id)
	}

	// Validate piece response: [index:4][begin:4][data]
	if len(payload) < 8 {
		t.Fatal("piece payload too short")
	}
	respIndex := binary.BigEndian.Uint32(payload[0:4])
	respBegin := binary.BigEndian.Uint32(payload[4:8])
	respData := payload[8:]

	if respIndex != 0 || respBegin != 0 {
		t.Fatalf("unexpected piece response: index=%d begin=%d", respIndex, respBegin)
	}
	if len(respData) != 16384 {
		t.Fatalf("expected 16384 bytes, got %d", len(respData))
	}

	// All bytes should be 0 (piece 0 is filled with byte(0))
	expected := make([]byte, 16384)
	if !bytes.Equal(respData, expected) {
		t.Fatal("piece data mismatch")
	}

	cancel()
	<-seederDone
}

func TestSeederRejectsWrongInfoHash(t *testing.T) {
	infoHash := [20]byte{1, 2, 3}
	wrongHash := [20]byte{4, 5, 6}

	bs := newTestBlockServer()
	bitfield := peer.NewBitField(3)
	s := New(infoHash, 3, bs, bitfield, nil, WithListenPort(0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.ListenPort()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Send handshake with wrong info hash
	if err := writeHandshake(conn, wrongHash); err != nil {
		t.Fatal(err)
	}

	// Connection should be closed by seeder — reading should fail
	buf := make([]byte, 68)
	_, err = io.ReadFull(conn, buf)
	if err == nil {
		t.Fatal("expected connection to be rejected")
	}
}

func TestChokerRespectsSlots(t *testing.T) {
	choker := NewChoker(2, 100*time.Millisecond)

	// Create 5 peers, all interested. Use net.Pipe and drain client sides
	// so writes don't block.
	peers := make([]*SeedPeer, 5)
	for i := range peers {
		client, server := net.Pipe()
		defer client.Close()
		defer server.Close()
		peers[i] = &SeedPeer{
			peer:       &peer.Peer{Conn: server, AmChoking: true},
			interested: true,
		}
		// Drain all data from client side so writes don't block
		go io.Copy(io.Discard, client)
	}

	choker.tick(peers)

	// Give a moment for writes to complete
	time.Sleep(10 * time.Millisecond)

	unchoked := 0
	for _, sp := range peers {
		if !sp.peer.AmChoking {
			unchoked++
		}
	}

	if unchoked != 2 {
		t.Fatalf("expected 2 unchoked peers, got %d", unchoked)
	}
}

func TestHaveBroadcast(t *testing.T) {
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	bs := newTestBlockServer()

	bitfield := peer.NewBitField(3)
	bitfield.SetPiece(0)
	// Pieces 1 and 2 not yet completed

	haveCh := make(chan int, 10)
	s := New(infoHash, 3, bs, bitfield, haveCh,
		WithListenPort(0),
		WithUnchokeSlots(10),
		WithUnchokeInterval(200*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Connect as client
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.ListenPort()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Handshake
	if err := writeHandshake(conn, infoHash); err != nil {
		t.Fatal(err)
	}
	if err := readHandshakeResponse(conn, infoHash); err != nil {
		t.Fatal(err)
	}

	// Read bitfield
	readMessage(conn)

	// Simulate piece 1 completing
	haveCh <- 1

	// Read Have message
	time.Sleep(50 * time.Millisecond)
	id, payload, err := readMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if id != internal.MessageHave {
		t.Fatalf("expected have (4), got %d", id)
	}
	if len(payload) < 4 {
		t.Fatal("have payload too short")
	}
	idx := binary.BigEndian.Uint32(payload[0:4])
	if idx != 1 {
		t.Fatalf("expected have for piece 1, got %d", idx)
	}
}

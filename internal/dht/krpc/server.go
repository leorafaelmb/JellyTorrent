package krpc

import (
	"log/slog"
	"net"
	"net/netip"
)

type Server struct {
	conn    net.PacketConn
	txns    *TransactionManager
	handler func(*Message, netip.AddrPort)
}

func NewServer(addr netip.AddrPort, handler func(*Message, netip.AddrPort)) (*Server, error) {
	txns := NewTransactionManager()

	conn, err := net.ListenPacket("udp", addr.String())
	if err != nil {
		return nil, err
	}

	return &Server{
		conn:    conn,
		txns:    txns,
		handler: handler,
	}, nil
}

func (s *Server) Start() {
	go s.readLoop()
}

func (s *Server) readLoop() {
	buf := make([]byte, 1500)
	for {
		n, addr, err := s.conn.ReadFrom(buf)
		if err != nil {
			return // socket closed, exit loop
		}

		msg, err := Unmarshal(buf[:n])
		if err != nil {
			slog.Debug("failed to unmarshal packet", "err", err, "addr", addr)
			continue
		}

		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		addrPort := udpAddr.AddrPort()

		switch msg.Type {
		case "r", "e":
			s.txns.Complete(msg.TransactionID, msg)
		case "q":
			s.handler(msg, addrPort)
		}
	}
}

func (s *Server) Send(msg *Message, addr netip.AddrPort) (string, <-chan *Message, error) {
	txnID, ch := s.txns.Add()
	msg.TransactionID = txnID

	data, err := Marshal(msg)
	if err != nil {
		s.txns.Cancel(txnID)
		return "", nil, err
	}

	_, err = s.conn.WriteTo(data, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		s.txns.Cancel(txnID)
		return "", nil, err
	}

	return txnID, ch, nil
}

func (s *Server) Cancel(txnID string) {
	s.txns.Cancel(txnID)
}

func (s *Server) Reply(msg *Message, addr netip.AddrPort) error {
	data, err := Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.conn.WriteTo(data, net.UDPAddrFromAddrPort(addr))
	return err
}

func (s *Server) LocalAddr() netip.AddrPort {
	addr := s.conn.LocalAddr().(*net.UDPAddr)
	return addr.AddrPort()
}

func (s *Server) Close() error {
	return s.conn.Close()
}

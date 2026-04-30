package udp

import (
	"fmt"
	"net"
)

type Sender struct {
	conn  *net.UDPConn
	raddr *net.UDPAddr
}

func NewSender(addr string, port int) (*Sender, error) {
	raddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", addr, port))
	if err != nil {
		return nil, fmt.Errorf("resolve udp addr %s:%d: %w", addr, port, err)
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s:%d: %w", addr, port, err)
	}
	return &Sender{conn: conn, raddr: raddr}, nil
}

func (s *Sender) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("udp close: %w", err)
	}
	return nil
}

func (s *Sender) Send(b []byte) (int, error) {
	n, err := s.conn.Write(b)
	if err != nil {
		return n, fmt.Errorf("udp write: %w", err)
	}
	return n, nil
}

type Receiver struct {
	conn *net.UDPConn
}

func Join(addr string, port int) (*Receiver, error) {
	bindAddr := addr
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}
	udpAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", bindAddr, port))
	if err != nil {
		return nil, fmt.Errorf("resolve udp addr %s:%d: %w", bindAddr, port, err)
	}

	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		if bindAddr != "0.0.0.0" {
			conn2, err2 := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
			if err2 == nil {
				conn = conn2
			} else {
				return nil, fmt.Errorf("listen on %s: %w; fallback 0.0.0.0: %w", udpAddr, err, err2)
			}
		} else {
			return nil, fmt.Errorf("listen on %s: %w", udpAddr, err)
		}
	}
	if err := conn.SetReadBuffer(1 << 20); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set udp read buffer: %w", err)
	}
	return &Receiver{conn: conn}, nil
}

func (r *Receiver) LocalPort() int {
	if r == nil || r.conn == nil {
		return 0
	}
	if a, ok := r.conn.LocalAddr().(*net.UDPAddr); ok {
		return a.Port
	}
	return 0
}

func (r *Receiver) Close() error {
	if err := r.conn.Close(); err != nil {
		return fmt.Errorf("udp close: %w", err)
	}
	return nil
}

func (r *Receiver) Read(b []byte) (int, *net.UDPAddr, error) {
	n, addr, err := r.conn.ReadFromUDP(b)
	if err != nil {
		return n, addr, fmt.Errorf("udp read: %w", err)
	}
	return n, addr, nil
}

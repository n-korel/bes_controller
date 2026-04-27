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
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, raddr: raddr}, nil
}

func (s *Sender) Close() error {
	return s.conn.Close()
}

func (s *Sender) Send(b []byte) (int, error) {
	return s.conn.Write(b)
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
		return nil, err
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
			return nil, err
		}
	}
	if err := conn.SetReadBuffer(1 << 20); err != nil {
		_ = conn.Close()
		return nil, err
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
	return r.conn.Close()
}

func (r *Receiver) Read(b []byte) (int, *net.UDPAddr, error) {
	return r.conn.ReadFromUDP(b)
}


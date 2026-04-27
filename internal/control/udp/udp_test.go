package udp

import (
	"bytes"
	"net"
	"testing"
)

func TestNilReceiverLocalPort(t *testing.T) {
	var r *Receiver
	if got := r.LocalPort(); got != 0 {
		t.Fatalf("LocalPort()=%d want 0", got)
	}
}

func TestJoinLoopbackSendRecv(t *testing.T) {
	recv, err := Join("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = recv.Close() }()

	if p := recv.LocalPort(); p == 0 {
		t.Fatal("expected ephemeral port")
	}

	s, err := NewSender("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	defer func() { _ = s.Close() }()

	want := []byte("hello-udp")
	if _, err := s.Send(want); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 2048)
	n, _, err := recv.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("got %q want %q", buf[:n], want)
	}
}

func TestJoinFallbackAllInterfaces(t *testing.T) {
	recv, err := Join("198.51.100.1", 0)
	if err != nil {
		t.Fatalf("Join (expect fallback to 0.0.0.0): %v", err)
	}
	defer func() { _ = recv.Close() }()

	if recv.LocalPort() == 0 {
		t.Fatal("expected bound port after fallback")
	}

	s, err := NewSender("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	defer func() { _ = s.Close() }()

	want := []byte("fallback")
	if _, err := s.Send(want); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 2048)
	n, raddr, err := recv.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("got %q want %q", buf[:n], want)
	}
	if !raddr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("unexpected src IP %v", raddr.IP)
	}
}

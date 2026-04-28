package udp

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestJoin_FallbackToAnyIPWhenSpecificBindFails(t *testing.T) {
	// 127.0.0.2 обычно не назначен на lo, поэтому bind часто падает EADDRNOTAVAIL.
	// Фолбэк должен попробовать 0.0.0.0 и успешно подняться (на port=0).
	r, err := Join("127.0.0.2", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = r.Close() }()

	if got := r.LocalPort(); got == 0 {
		t.Fatalf("LocalPort=0; want non-zero")
	}
}

func TestJoin_FallbackErrorIncludesBothFailures(t *testing.T) {
	// Занимаем порт на 0.0.0.0, чтобы и основной bind (на 127.0.0.1), и fallback (0.0.0.0) упали с EADDRINUSE.
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = l.Close() }()
	port := l.LocalAddr().(*net.UDPAddr).Port

	_, err = Join("127.0.0.1", port)
	if err == nil {
		t.Fatalf("Join: expected error")
	}
	if !strings.Contains(err.Error(), "fallback 0.0.0.0") {
		t.Fatalf("error=%q; want mention fallback 0.0.0.0", err.Error())
	}
	if !strings.Contains(err.Error(), "address already in use") {
		// net.ListenUDP обычно возвращает syscall.EADDRINUSE, текст может зависеть от платформы.
		t.Fatalf("error=%q; want EADDRINUSE-ish", err.Error())
	}
}

func TestReceiverLocalPort_NilSafety(t *testing.T) {
	var r *Receiver
	if got := r.LocalPort(); got != 0 {
		t.Fatalf("nil receiver LocalPort=%d; want 0", got)
	}

	r = &Receiver{conn: nil}
	if got := r.LocalPort(); got != 0 {
		t.Fatalf("nil conn LocalPort=%d; want 0", got)
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

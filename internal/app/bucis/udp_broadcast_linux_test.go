//go:build linux

package bucis

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestBUCIS_BroadcastSend_DoesNotFailWithEACCES(t *testing.T) {
	raddr := &net.UDPAddr{IP: net.IPv4bcast, Port: 8890}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := enableUDPBroadcast(conn); err != nil {
		if errors.Is(err, syscall.EACCES) {
			t.Fatalf("enableUDPBroadcast returned EACCES: %v", err)
		}
		t.Fatalf("enableUDPBroadcast: %v", err)
	}

	_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = conn.Write([]byte("test"))
	if err != nil && errors.Is(err, syscall.EACCES) {
		t.Fatalf("broadcast write returned EACCES: %v", err)
	}
}

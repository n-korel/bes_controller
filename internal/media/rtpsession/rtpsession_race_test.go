package rtpsession_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"bucis-bes_simulator/internal/media/receiver"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/sender"
)

func TestSharedConn_RXStopMakesTXWriteFailWithErrClosed(t *testing.T) {
	rx := receiver.New(0)
	if err := rx.Start(); err != nil {
		t.Fatalf("rx.Start(): %v", err)
	}
	conn := rx.Conn()
	if conn == nil {
		t.Fatalf("rx.Conn() is nil")
	}

	remote, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(remote): %v", err)
	}
	defer func() { _ = remote.Close() }()

	remoteAddr, ok := remote.LocalAddr().(*net.UDPAddr)
	if !ok || remoteAddr == nil {
		t.Fatalf("remote.LocalAddr(): %T", remote.LocalAddr())
	}

	tx, err := sender.NewFromConn(conn, remoteAddr)
	if err != nil {
		t.Fatalf("sender.NewFromConn(): %v", err)
	}
	defer func() { _ = tx.Close() }()

	if _, err := rx.Stop(); err != nil {
		t.Fatalf("rx.Stop(): %v", err)
	}

	pcm := make([]int16, rtp.SamplesPerFrame)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err = tx.StreamAt(ctx, time.Now().UnixMilli(), pcm)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected net.ErrClosed, got: %v", err)
	}
}

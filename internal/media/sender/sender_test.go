package sender

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/udp"
)

func TestStreamAt_EmptyPCMNoOp(t *testing.T) {
	recv, err := udp.Join("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = recv.Close() }()

	s, err := New("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	if err := s.StreamAt(ctx, time.Now().UnixMilli(), nil); err != nil {
		t.Fatalf("StreamAt nil: %v", err)
	}
	if err := s.StreamAt(ctx, time.Now().UnixMilli(), []int16{}); err != nil {
		t.Fatalf("StreamAt empty: %v", err)
	}
}

func TestStreamAt_CancelBeforeStartWaits(t *testing.T) {
	recv, err := udp.Join("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = recv.Close() }()

	s, err := New("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pcm := make([]int16, 160)
	t0 := time.Now().Add(time.Hour).UnixMilli()
	if err := s.StreamAt(ctx, t0, pcm); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v want %v", err, context.Canceled)
	}
}

func TestStreamAt_OneFrameRoundTrip(t *testing.T) {
	recv, err := udp.Join("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = recv.Close() }()

	s, err := New("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	pcm := make([]int16, 160)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	if err := s.StreamAt(ctx, t0, pcm); err != nil {
		t.Fatalf("StreamAt: %v", err)
	}

	buf := make([]byte, 2048)
	n, _, err := recv.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n < 12 {
		t.Fatalf("rtp packet too short: %d", n)
	}
}

func TestStreamAt_ClosedReturnsErrClosed(t *testing.T) {
	recv, err := udp.Join("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	defer func() { _ = recv.Close() }()

	s, err := New("127.0.0.1", recv.LocalPort())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pcm := make([]int16, 160*20)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.StreamAt(ctx, t0, pcm)
	}()

	time.Sleep(80 * time.Millisecond)
	_ = s.Close()

	err = <-errCh
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("got %v want %v", err, net.ErrClosed)
	}
}

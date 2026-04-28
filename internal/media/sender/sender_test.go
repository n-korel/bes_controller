package sender

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/udp"
	mediarpt "bucis-bes_simulator/internal/media/rtp"

	pionrtp "github.com/pion/rtp"
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

func TestStreamFramesAt_NilFrameStopsWithoutSending(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))

	port := conn.LocalAddr().(*net.UDPAddr).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	frames := make(chan []int16, 1)
	frames <- nil
	close(frames)

	ctx := context.Background()
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	if err := s.StreamFramesAt(ctx, t0, frames); err != nil {
		t.Fatalf("StreamFramesAt: %v", err)
	}

	buf := make([]byte, 2048)
	if _, _, err := conn.ReadFromUDP(buf); err == nil {
		t.Fatalf("expected no packets, but received one")
	}
}

func TestStreamFramesAt_PadsAndTruncatesTo160Samples(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	frames := make(chan []int16, 3)
	frames <- make([]int16, 10)   // padded
	frames <- make([]int16, 200)  // truncated
	frames <- nil                 // stop
	close(frames)

	ctx := context.Background()
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	if err := s.StreamFramesAt(ctx, t0, frames); err != nil {
		t.Fatalf("StreamFramesAt: %v", err)
	}

	readPacket := func() pionrtp.Packet {
		t.Helper()
		buf := make([]byte, 2048)
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		var pkt pionrtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		return pkt
	}

	p1 := readPacket()
	p2 := readPacket()

	if p1.PayloadType != mediarpt.PayloadTypeG726() || p2.PayloadType != mediarpt.PayloadTypeG726() {
		t.Fatalf("unexpected payload type: p1=%d p2=%d", p1.PayloadType, p2.PayloadType)
	}
	if len(p1.Payload) != 80 || len(p2.Payload) != 80 {
		t.Fatalf("payload lengths: p1=%d p2=%d want 80", len(p1.Payload), len(p2.Payload))
	}
	if got, want := p2.Timestamp, p1.Timestamp+uint32(mediarpt.SamplesPerFrame); got != want {
		t.Fatalf("timestamp delta: got %d want %d", got, want)
	}
	if got, want := p2.SequenceNumber, p1.SequenceNumber+1; got != want {
		t.Fatalf("seq delta: got %d want %d", got, want)
	}
}

func TestStreamFramesAt_CancelDuringStreaming_ReturnsContextCanceled(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	frames := make(chan []int16, 100)
	for i := 0; i < 100; i++ {
		frames <- make([]int16, mediarpt.SamplesPerFrame)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	go func() {
		errCh <- s.StreamFramesAt(ctx, t0, frames)
	}()

	// Wait for first packet to ensure we're inside the send loop.
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadFromUDP(buf); err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v want %v", err, context.Canceled)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for StreamFramesAt to return after cancel")
	}
}

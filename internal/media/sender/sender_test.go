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

func localUDPAddr(t *testing.T, c *net.UDPConn) *net.UDPAddr {
	t.Helper()
	la := c.LocalAddr()
	ua, ok := la.(*net.UDPAddr)
	if !ok {
		t.Fatalf("LocalAddr: want *net.UDPAddr, got %T", la)
	}
	return ua
}

func TestSender_write_UsesWriteWhenConnected(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = srv.Close() }()

	s, err := NewTo(localUDPAddr(t, srv))
	if err != nil {
		t.Fatalf("NewTo: %v", err)
	}
	defer func() { _ = s.Close() }()

	payload := []byte("hello")
	if _, err := s.write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	_ = srv.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, _, err := srv.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if got := string(buf[:n]); got != string(payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

func TestSender_write_UsesWriteToUDPWhenNotConnected(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = srv.Close() }()

	lconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP local: %v", err)
	}
	defer func() { _ = lconn.Close() }()

	s, err := NewFromConn(lconn, localUDPAddr(t, srv))
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}
	defer func() { _ = s.Close() }()

	payload := []byte("hi")
	if _, err := s.write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	_ = srv.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, _, err := srv.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if got := string(buf[:n]); got != string(payload) {
		t.Fatalf("got %q want %q", got, payload)
	}
}

func TestSender_StreamAt_RespectsCtxDuringInitialWait(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = srv.Close() }()

	s, err := NewTo(localUDPAddr(t, srv))
	if err != nil {
		t.Fatalf("NewTo: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pcm := make([]int16, mediarpt.SamplesPerFrame)
	err = s.StreamAt(ctx, time.Now().Add(5*time.Second).UnixMilli(), pcm)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v; want %v", err, context.Canceled)
	}
}

func TestSender_StreamFramesAt_CloseDuringStreamReturnsErrClosed(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = srv.Close() }()

	s, err := NewTo(localUDPAddr(t, srv))
	if err != nil {
		t.Fatalf("NewTo: %v", err)
	}

	frames := make(chan []int16, 16)
	frames <- make([]int16, mediarpt.SamplesPerFrame)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.StreamFramesAt(ctx, time.Now().UnixMilli(), frames)
	}()

	_ = s.Close()
	err = <-done
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("err=%v; want net.ErrClosed", err)
	}
}

func TestSender_StreamFramesAt_PadsAndTrimsFrames(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = srv.Close() }()

	s, err := NewTo(localUDPAddr(t, srv))
	if err != nil {
		t.Fatalf("NewTo: %v", err)
	}
	defer func() { _ = s.Close() }()

	frames := make(chan []int16, 4)
	frames <- make([]int16, mediarpt.SamplesPerFrame-10) // pad
	frames <- make([]int16, mediarpt.SamplesPerFrame+10) // trim
	close(frames)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.StreamFramesAt(ctx, time.Now().UnixMilli(), frames); err != nil {
		t.Fatalf("StreamFramesAt: %v", err)
	}

	// Должны были уйти минимум 2 пакета (один на padded, один на trimmed).
	buf := make([]byte, 2048)
	for i := 0; i < 2; i++ {
		_ = srv.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if _, _, err := srv.ReadFromUDP(buf); err != nil {
			t.Fatalf("ReadFromUDP[%d]: %v", i, err)
		}
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

	port := localUDPAddr(t, conn).Port
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

	port := localUDPAddr(t, conn).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	frames := make(chan []int16, 3)
	frames <- make([]int16, 10)  // padded
	frames <- make([]int16, 200) // truncated
	frames <- nil                // stop
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

	port := localUDPAddr(t, conn).Port
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

func TestStreamFramesAt_FramesNil_SendsSilenceUntilCancel(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	port := localUDPAddr(t, conn).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	go func() {
		errCh <- s.StreamFramesAt(ctx, t0, nil)
	}()

	// Ensure we receive at least one packet (silence frame).
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	var pkt pionrtp.Packet
	if err := pkt.Unmarshal(buf[:n]); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if pkt.PayloadType != mediarpt.PayloadTypeG726() {
		t.Fatalf("payload type=%d want %d", pkt.PayloadType, mediarpt.PayloadTypeG726())
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

func TestNewFromConn_UsesWriteToUDP_WhenNotConnected(t *testing.T) {
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = recv.Close() }()
	recvPort := localUDPAddr(t, recv).Port

	local, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(local): %v", err)
	}
	defer func() { _ = local.Close() }()

	remote := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: recvPort}
	s, err := NewFromConn(local, remote)
	if err != nil {
		t.Fatalf("NewFromConn: %v", err)
	}
	defer func() { _ = s.Close() }()

	pcm := make([]int16, mediarpt.SamplesPerFrame)
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	if err := s.StreamAt(context.Background(), t0, pcm); err != nil {
		t.Fatalf("StreamAt: %v", err)
	}

	buf := make([]byte, 2048)
	_ = recv.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	n, _, err := recv.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	var pkt pionrtp.Packet
	if err := pkt.Unmarshal(buf[:n]); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if pkt.PayloadType != mediarpt.PayloadTypeG726() {
		t.Fatalf("payload type=%d want %d", pkt.PayloadType, mediarpt.PayloadTypeG726())
	}
}

func TestStreamAt_CancelDuringStreaming_ReturnsContextCanceled(t *testing.T) {
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = recv.Close() }()

	port := localUDPAddr(t, recv).Port
	s, err := New("127.0.0.1", port)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	pcm := make([]int16, mediarpt.SamplesPerFrame*40)
	t0 := time.Now().Add(-time.Millisecond).UnixMilli()
	go func() {
		errCh <- s.StreamAt(ctx, t0, pcm)
	}()

	// дождёмся первого RTP-пакета, чтобы отмена пришла "внутри" цикла отправки
	buf := make([]byte, 2048)
	_ = recv.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err = recv.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v want %v", err, context.Canceled)
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatalf("timeout waiting for StreamAt to return after cancel")
	}
}

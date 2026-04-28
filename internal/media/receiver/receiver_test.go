package receiver

import (
	"net"
	"testing"
	"time"

	mediarpt "bucis-bes_simulator/internal/media/rtp"

	pionrtp "github.com/pion/rtp"
)

func TestReceiver_StopWithoutStart_NoHang(t *testing.T) {
	r := New(0)
	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stats.Received != 0 {
		t.Fatalf("stats.Received=%d want 0", stats.Received)
	}
}

func TestReceiver_StartTwice_DoesNotRestart(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("StartBySoundType(99): %v", err)
	}
	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("StartBySoundType(99) second: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stats.Received == 0 {
		t.Fatalf("stats.Received=0 want >0")
	}
	if r.IsPlaying() {
		t.Fatalf("IsPlaying=true want false")
	}
}

func TestReceiver_UpdateStats_NoOpWhenNotPlaying(t *testing.T) {
	r := &Receiver{playing: false}
	r.updateStats(1, 1)
	if r.stats.Received != 0 {
		t.Fatalf("stats.Received=%d want 0", r.stats.Received)
	}
}

func TestReceiver_RTP_StopWithoutPackets_ReturnsQuicklyAndCleansConn(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	if r.Conn() == nil {
		_, _ = r.Stop()
		t.Fatalf("Conn()=nil want non-nil after start")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Stop()
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Stop timed out (likely blocked in ReadFromUDP)")
	}

	if r.IsPlaying() {
		t.Fatalf("IsPlaying=true want false")
	}
	if r.Conn() != nil {
		t.Fatalf("Conn() non-nil after Stop, want nil")
	}
}

func TestReceiver_RTP_RestartAfterStop_Works(t *testing.T) {
	r := New(0)

	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port1 := r.MediaPort()
	if port1 == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero")
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1) second: %v", err)
	}
	port2 := r.MediaPort()
	if port2 == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero on restart")
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop second: %v", err)
	}
}

func TestReceiver_StartBySoundType_Unknown_DoesNotStart(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(12345); err != nil {
		t.Fatalf("StartBySoundType(unknown): %v", err)
	}
	if r.IsPlaying() {
		t.Fatalf("IsPlaying=true want false for unknown sound type")
	}
	if r.Conn() != nil {
		t.Fatalf("Conn() non-nil want nil for unknown sound type")
	}
}

func TestReceiver_RTP_StopDuringActiveReceive_ReturnsAndStatsNonZero(t *testing.T) {
	r := New(0)
	gotPCM := make(chan []int16, 8)
	r.SetPCMSink(func(pcm []int16) {
		select {
		case gotPCM <- pcm:
		default:
		}
	})

	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port := r.MediaPort()
	if port == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero")
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendPkt := func(seq uint16, ts uint32) {
		t.Helper()
		pkt := &pionrtp.Packet{
			Header: pionrtp.Header{
				Version:        2,
				PayloadType:    mediarpt.PayloadTypeG726(),
				SequenceNumber: seq,
				Timestamp:      ts,
				SSRC:           0x01020304,
			},
			Payload: []byte{0x11, 0x22, 0x33, 0x44},
		}
		raw, err := pkt.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		_, _ = conn.Write(raw)
	}

	// отправим несколько пакетов, чтобы гарантировать работу цикла чтения
	for i := 0; i < 3; i++ {
		sendPkt(uint16(100+i), uint32(160*(i+1)))
	}

	// дождёмся хотя бы одного PCM callback (или таймаут)
	select {
	case <-gotPCM:
	case <-time.After(500 * time.Millisecond):
		// если PCM callback не пришёл, всё равно попробуем Stop и проверим статистику
	}

	done := make(chan struct{})
	var stats SessionStats
	var stopErr error
	go func() {
		defer close(done)
		stats, stopErr = r.Stop()
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Stop timed out during active receive")
	}
	if stopErr != nil {
		t.Fatalf("Stop: %v", stopErr)
	}
	if stats.Received == 0 {
		t.Fatalf("stats.Received=0 want >0")
	}
	if stats.LastPacket.IsZero() {
		t.Fatalf("stats.LastPacket is zero want non-zero")
	}
}

func TestReceiver_RTP_ExpectedAndLost_WithSequenceGap(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port := r.MediaPort()
	if port == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero")
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendPkt := func(seq uint16, ts uint32) {
		t.Helper()
		pkt := &pionrtp.Packet{
			Header: pionrtp.Header{
				Version:        2,
				PayloadType:    mediarpt.PayloadTypeG726(),
				SequenceNumber: seq,
				Timestamp:      ts,
				SSRC:           0x01020304,
			},
			Payload: []byte{0x11, 0x22, 0x33, 0x44},
		}
		raw, err := pkt.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		_, _ = conn.Write(raw)
	}

	// seq gap: 10, 11, 13 -> expected = 4, received = 3, lost = 1
	sendPkt(10, 160)
	sendPkt(11, 320)
	sendPkt(13, 640)

	// Дадим горутине приёма чуть времени обработать пакеты.
	time.Sleep(50 * time.Millisecond)

	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, want := stats.Received, uint32(3); got != want {
		t.Fatalf("stats.Received=%d want %d", got, want)
	}
	if got, want := stats.Expected(), uint32(4); got != want {
		t.Fatalf("stats.Expected()=%d want %d", got, want)
	}
	if got, want := stats.Lost(), uint32(1); got != want {
		t.Fatalf("stats.Lost()=%d want %d", got, want)
	}
}

func TestReceiver_RTP_WrapAroundSequence_IncreasesCycles(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port := r.MediaPort()
	if port == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero")
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	sendPkt := func(seq uint16, ts uint32) {
		t.Helper()
		pkt := &pionrtp.Packet{
			Header: pionrtp.Header{
				Version:        2,
				PayloadType:    mediarpt.PayloadTypeG726(),
				SequenceNumber: seq,
				Timestamp:      ts,
				SSRC:           0x01020304,
			},
			Payload: []byte{0x11, 0x22, 0x33, 0x44},
		}
		raw, err := pkt.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		_, _ = conn.Write(raw)
	}

	// wrap-around: 65534, 65535, 0, 1 with newer RTP timestamps
	sendPkt(65534, 160)
	sendPkt(65535, 320)
	sendPkt(0, 480)
	sendPkt(1, 640)

	time.Sleep(60 * time.Millisecond)

	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, want := stats.Received, uint32(4); got != want {
		t.Fatalf("stats.Received=%d want %d", got, want)
	}
	if got, want := stats.Cycles, uint32(1<<16); got != want {
		t.Fatalf("stats.Cycles=%d want %d", got, want)
	}
	if got, want := stats.Expected(), uint32(4); got != want {
		t.Fatalf("stats.Expected()=%d want %d", got, want)
	}
	if got, want := stats.Lost(), uint32(0); got != want {
		t.Fatalf("stats.Lost()=%d want %d", got, want)
	}
}

func TestLatePacketDoesNotIncreaseCycles(t *testing.T) {
	r := &Receiver{playing: true}

	r.updateStats(40000, 1000)

	r.updateStats(50000, 2000)

	r.updateStats(1000, 1500)

	if r.stats.Cycles != 0 {
		t.Fatalf("expected Cycles=0, got %d", r.stats.Cycles)
	}
	if r.stats.MaxSeq != 50000 {
		t.Fatalf("expected MaxSeq=50000, got %d", r.stats.MaxSeq)
	}
	if got, want := r.stats.Expected(), uint32(10001); got != want {
		t.Fatalf("expected Expected=%d, got %d", want, got)
	}
	if got, want := r.stats.Lost(), uint32(9998); got != want {
		t.Fatalf("expected Lost=%d, got %d", want, got)
	}
}

func TestLatePacketWithNewerTimestampAfterSourceResetDoesNotIncreaseCycles(t *testing.T) {
	r := &Receiver{playing: true}

	r.updateStats(40000, 1000)
	r.updateStats(50000, 2000)
	r.updateStats(1000, 3000)

	if r.stats.Cycles != 0 {
		t.Fatalf("expected Cycles=0, got %d", r.stats.Cycles)
	}
	if r.stats.MaxSeq != 50000 {
		t.Fatalf("expected MaxSeq=50000, got %d", r.stats.MaxSeq)
	}
	if got, want := r.stats.Expected(), uint32(10001); got != want {
		t.Fatalf("expected Expected=%d, got %d", want, got)
	}
}

func TestWrapSequenceIncreasesCycles(t *testing.T) {
	r := &Receiver{playing: true}

	r.updateStats(60000, 1000)

	r.updateStats(65000, 1500)

	r.updateStats(100, 2000)
	
	r.updateStats(60500, 1600)

	if got, want := r.stats.Cycles, uint32(1<<16); got != want {
		t.Fatalf("expected Cycles=%d, got %d", want, got)
	}
	if got, want := r.stats.MaxSeq, uint16(100); got != want {
		t.Fatalf("expected MaxSeq=%d, got %d", want, got)
	}
	if got, want := r.stats.Expected(), uint32(5637); got != want {
		t.Fatalf("expected Expected=%d, got %d", want, got)
	}
}

func TestReceiver_SimMic_StartStop_UpdatesStats(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("StartBySoundType(99): %v", err)
	}
	if !r.IsPlaying() {
		t.Fatalf("IsPlaying=false want true")
	}

	time.Sleep(60 * time.Millisecond)

	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if r.IsPlaying() {
		t.Fatalf("IsPlaying=true want false")
	}
	if stats.Received == 0 {
		t.Fatalf("stats.Received=0 want >0")
	}
	if got := r.LastPacketAt(); got.IsZero() {
		t.Fatalf("LastPacketAt is zero want non-zero")
	}
}

func TestReceiver_RTP_StartStop_DeliversPCMAndUpdatesStats(t *testing.T) {
	r := New(0)
	gotPCM := make(chan []int16, 1)
	r.SetPCMSink(func(pcm []int16) {
		select {
		case gotPCM <- pcm:
		default:
		}
	})

	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port := r.MediaPort()
	if port == 0 {
		_, _ = r.Stop()
		t.Fatalf("MediaPort=0 want non-zero")
	}

	pkt := &pionrtp.Packet{
		Header: pionrtp.Header{
			Version:        2,
			PayloadType:    mediarpt.PayloadTypeG726(),
			SequenceNumber: 1,
			Timestamp:      160,
			SSRC:           0x01020304,
		},
		Payload: []byte{0x11, 0x22, 0x33, 0x44},
	}
	raw, err := pkt.Marshal()
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("Marshal: %v", err)
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("DialUDP: %v", err)
	}
	_, _ = conn.Write(raw)
	_ = conn.Close()

	select {
	case pcm := <-gotPCM:
		if len(pcm) == 0 {
			_, _ = r.Stop()
			t.Fatalf("pcm is empty want non-empty")
		}
	case <-time.After(500 * time.Millisecond):
		_, _ = r.Stop()
		t.Fatalf("timeout waiting for pcm")
	}

	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stats.Received == 0 {
		t.Fatalf("stats.Received=0 want >0")
	}
	if stats.Expected() == 0 {
		t.Fatalf("stats.Expected=0 want >0")
	}
}

func TestReceiver_RTP_IgnoresWrongPayloadTypeAndBadPackets(t *testing.T) {
	r := New(0)
	called := make(chan struct{}, 1)
	r.SetPCMSink(func(pcm []int16) {
		select {
		case called <- struct{}{}:
		default:
		}
	})

	if err := r.StartBySoundType(1); err != nil {
		t.Fatalf("StartBySoundType(1): %v", err)
	}
	port := r.MediaPort()
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	badPkt := []byte{0x01, 0x02, 0x03, 0x04}
	_, _ = conn.Write(badPkt)

	pktWrongPT := &pionrtp.Packet{
		Header: pionrtp.Header{
			Version:        2,
			PayloadType:    mediarpt.PayloadTypeG726() + 1,
			SequenceNumber: 1,
			Timestamp:      160,
			SSRC:           0x01020304,
		},
		Payload: []byte{0x11, 0x22},
	}
	rawWrongPT, err := pktWrongPT.Marshal()
	if err != nil {
		_, _ = r.Stop()
		t.Fatalf("Marshal: %v", err)
	}
	_, _ = conn.Write(rawWrongPT)

	select {
	case <-called:
		_, _ = r.Stop()
		t.Fatalf("pcmSink was called, want not called")
	case <-time.After(200 * time.Millisecond):
	}

	stats, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stats.Received != 0 {
		t.Fatalf("stats.Received=%d want 0", stats.Received)
	}
}

func TestSessionStats_ExpectedLostEdgeCases(t *testing.T) {
	var s SessionStats
	if got := s.Expected(); got != 0 {
		t.Fatalf("Expected()=%d want 0", got)
	}
	if got := s.Lost(); got != 0 {
		t.Fatalf("Lost()=%d want 0", got)
	}

	s = SessionStats{FirstSeq: 10, MaxSeq: 12, Cycles: 0, Received: 3}
	if got, want := s.Expected(), uint32(3); got != want {
		t.Fatalf("Expected()=%d want %d", got, want)
	}
	if got := s.Lost(); got != 0 {
		t.Fatalf("Lost()=%d want 0", got)
	}

	s = SessionStats{FirstSeq: 10, MaxSeq: 12, Cycles: 0, Received: 2}
	if got, want := s.Expected(), uint32(3); got != want {
		t.Fatalf("Expected()=%d want %d", got, want)
	}
	if got, want := s.Lost(), uint32(1); got != want {
		t.Fatalf("Lost()=%d want %d", got, want)
	}
}

func TestSessionStats_JitterMs(t *testing.T) {
	s := SessionStats{Jitter: 80}
	if got, want := s.JitterMs(), 10.0; got != want {
		t.Fatalf("JitterMs()=%v want %v", got, want)
	}
}

func TestIsNewerTimestamp_WrapAround(t *testing.T) {
	// b is near uint32 max, a wrapped and is slightly "newer" in RTP terms.
	var b uint32 = 0xfffffff0
	var a uint32 = 0x00000010
	if !isNewerTimestamp(a, b) {
		t.Fatalf("expected a newer than b under wrap-around")
	}
	if isNewerTimestamp(b, a) {
		t.Fatalf("expected b not newer than a under wrap-around")
	}
}

func TestWrapSequence_DoesNotIncreaseCyclesIfGapTooLarge(t *testing.T) {
	r := &Receiver{playing: true}

	r.updateStats(60000, 1000)
	r.updateStats(65000, 2000)

	// seq wrapped, timestamp is newer, but the forward gap is too large to accept wrap.
	r.updateStats(20000, 3000)

	if got, want := r.stats.Cycles, uint32(0); got != want {
		t.Fatalf("Cycles=%d want %d", got, want)
	}
	if got, want := r.stats.MaxSeq, uint16(65000); got != want {
		t.Fatalf("MaxSeq=%d want %d", got, want)
	}
}


package receiver

import (
	"net"
	"testing"
	"time"

	mediarpt "bucis-bes_simulator/internal/media/rtp"

	pionrtp "github.com/pion/rtp"
)

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


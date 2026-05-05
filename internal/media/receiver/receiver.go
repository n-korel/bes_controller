package receiver

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"bucis-bes_simulator/internal/media/g726"
	mediarpt "bucis-bes_simulator/internal/media/rtp"

	pionrtp "github.com/pion/rtp"
)

const (
	readTimeout       = 200 * time.Millisecond
	rtpTickNs         = int64(125000)
	maxWrapForwardGap = 10000
)

type SessionStats struct {
	FirstSeq    uint16
	LastSeq     uint16
	MaxSeq      uint16
	Cycles      uint32
	Received    uint32
	Jitter      float64
	LastArrival int64
	LastRTPTs   uint32
	MaxRTPTs    uint32
	LastPacket  time.Time
}

func (s SessionStats) Expected() uint32 {
	if s.Received == 0 {
		return 0
	}
	extMax := s.Cycles + uint32(s.MaxSeq)
	return extMax - uint32(s.FirstSeq) + 1
}

func (s SessionStats) Lost() uint32 {
	expected := s.Expected()
	if expected <= s.Received {
		return 0
	}
	return expected - s.Received
}

func (s SessionStats) JitterMs() float64 {
	return s.Jitter / 8.0
}

type Receiver struct {
	mediaPort int

	mu      sync.Mutex
	playing bool
	mode    int
	stopCh  chan struct{}
	doneCh  chan struct{}
	stats   SessionStats
	runErr  error
	pcmSink func(pcm []int16)

	connMu sync.Mutex
	conn   *net.UDPConn
}

const (
	modeRTP = iota + 1
	modeSimMic
)

func New(mediaPort int) *Receiver {
	return &Receiver{
		mediaPort: mediaPort,
	}
}

func (r *Receiver) SetPCMSink(fn func(pcm []int16)) {
	r.mu.Lock()
	r.pcmSink = fn
	r.mu.Unlock()
}

func (r *Receiver) Start() error {
	return r.StartBySoundType(1)
}

// StartFromConn starts the receiver using an already-open UDP connection.
// The conn must be bound to the desired local port; the receiver takes ownership
// and will close it on Stop().
func (r *Receiver) StartFromConn(conn *net.UDPConn) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	r.mu.Lock()
	if r.doneCh != nil || r.playing {
		r.mu.Unlock()
		return nil
	}
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		r.mediaPort = a.Port
	}
	r.connMu.Lock()
	r.conn = conn
	r.connMu.Unlock()
	r.mode = modeRTP
	r.playing = true
	r.runErr = nil
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.stats = SessionStats{}
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.mu.Unlock()

	go r.runRTP(conn, stopCh, doneCh)
	return nil
}

func (r *Receiver) StartBySoundType(soundType int) error {
	r.mu.Lock()
	if r.doneCh != nil || r.playing {
		r.mu.Unlock()
		return nil
	}

	switch soundType {
	case 1:
		laddr := &net.UDPAddr{IP: net.IPv4zero, Port: r.mediaPort}
		conn, err := net.ListenUDP("udp4", laddr)
		if err != nil {
			r.mu.Unlock()
			return fmt.Errorf("listen udp media port %d: %w", r.mediaPort, err)
		}
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			r.mediaPort = a.Port
		}
		r.connMu.Lock()
		r.conn = conn
		r.connMu.Unlock()
		r.mode = modeRTP
	case 2:
		laddr := &net.UDPAddr{IP: net.IPv4zero, Port: r.mediaPort}
		conn, err := net.ListenUDP("udp4", laddr)
		if err != nil {
			r.mu.Unlock()
			return fmt.Errorf("listen udp media port %d: %w", r.mediaPort, err)
		}
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			r.mediaPort = a.Port
		}
		r.connMu.Lock()
		r.conn = conn
		r.connMu.Unlock()
		r.mode = modeRTP
	case 99:
		// Test-only mode: synthetic "mic" stats without networking.
		r.connMu.Lock()
		r.conn = nil
		r.connMu.Unlock()
		r.mode = modeSimMic
	default:
		r.mu.Unlock()
		return nil
	}

	r.playing = true
	r.runErr = nil
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
	r.stats = SessionStats{}
	stopCh := r.stopCh
	doneCh := r.doneCh
	mode := r.mode
	conn := r.conn
	r.mu.Unlock()

	switch mode {
	case modeRTP:
		go r.runRTP(conn, stopCh, doneCh)
	case modeSimMic:
		go r.runSimMic(stopCh, doneCh)
	default:
		close(doneCh)
	}
	return nil
}

func (r *Receiver) MediaPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mediaPort
}

func (r *Receiver) Conn() *net.UDPConn {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.conn
}

func (r *Receiver) Stop() (SessionStats, error) {
	r.mu.Lock()
	if r.doneCh == nil {
		stats := r.stats
		err := r.runErr
		r.runErr = nil
		r.playing = false
		r.mu.Unlock()
		return stats, err
	}
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.mu.Unlock()

	r.connMu.Lock()
	if c := r.conn; c != nil {
		_ = c.SetReadDeadline(time.Now())
	}
	r.connMu.Unlock()

	close(stopCh)

	<-doneCh

	r.mu.Lock()
	stats := r.stats
	err := r.runErr
	r.runErr = nil
	r.playing = false
	r.mode = 0
	r.stopCh = nil
	r.doneCh = nil
	r.mu.Unlock()
	return stats, err
}

func (r *Receiver) IsPlaying() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.playing
}

func (r *Receiver) LastPacketAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats.LastPacket
}

func (r *Receiver) StatsSnapshot() SessionStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func (r *Receiver) runRTP(conn *net.UDPConn, stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer func() {
		r.mu.Lock()
		r.playing = false
		r.mu.Unlock()
		close(doneCh)
	}()
	defer func() {
		r.connMu.Lock()
		r.conn = nil
		r.connMu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	}()
	_ = conn.SetReadBuffer(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	buf := make([]byte, 2048)
	decState := &g726.G726DecoderState{}

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
				continue
			}
			r.mu.Lock()
			r.runErr = err
			r.mu.Unlock()
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

		var pkt pionrtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		if pkt.PayloadType != mediarpt.PayloadTypeG726() {
			continue
		}

		pcm := g726.G726DecodeFrame(pkt.Payload, decState)
		r.mu.Lock()
		sink := r.pcmSink
		r.mu.Unlock()
		if sink != nil {
			sink(pcm)
		}
		r.updateStats(pkt.SequenceNumber, pkt.Timestamp)
	}
}

func (r *Receiver) runSimMic(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer func() {
		r.mu.Lock()
		r.playing = false
		r.mu.Unlock()
		close(doneCh)
	}()

	const (
		frameDur = 20 * time.Millisecond
		frameTs  = 160 // 20 ms @ 8 kHz
	)
	ticker := time.NewTicker(frameDur)
	defer ticker.Stop()

	var seq uint16
	var ts uint32

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			r.updateStats(seq, ts)
			seq++
			ts += frameTs
		}
	}
}

func (r *Receiver) updateStats(seq uint16, rtpTs uint32) {
	now := time.Now()
	nowRTPUnits := now.UnixNano() / rtpTickNs

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.playing {
		return
	}
	s := r.stats

	s.LastPacket = now
	if s.Received == 0 {
		s.FirstSeq = seq
		s.LastSeq = seq
		s.MaxSeq = seq
		s.Cycles = 0
		s.Received = 1
		s.LastArrival = nowRTPUnits
		s.LastRTPTs = rtpTs
		s.MaxRTPTs = rtpTs
		r.stats = s
		return
	}

	arrivalDelta := nowRTPUnits - s.LastArrival
	tsDelta := int64(int32(rtpTs - s.LastRTPTs))
	d := arrivalDelta - tsDelta
	if d < 0 {
		d = -d
	}
	s.Jitter += (float64(d) - s.Jitter) / 16.0
	s.LastArrival = nowRTPUnits
	s.LastRTPTs = rtpTs

	if isNewerTimestamp(rtpTs, s.MaxRTPTs) {
		extMax := s.Cycles + uint32(s.MaxSeq)
		u0 := s.Cycles + uint32(seq)
		if u0 > extMax {
			s.MaxSeq = seq
			s.MaxRTPTs = rtpTs
		} else {
			u1 := u0 + (1 << 16)
			if u1 > extMax && (u1-extMax) <= maxWrapForwardGap {
				s.Cycles += 1 << 16
				s.MaxSeq = seq
				s.MaxRTPTs = rtpTs
			}
		}
	}
	s.LastSeq = seq
	s.Received++

	r.stats = s
}

func isNewerTimestamp(a, b uint32) bool {
	return int32(a-b) > 0
}

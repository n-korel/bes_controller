package sender

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"bucis-bes_simulator/internal/media/g726"
	mediarpt "bucis-bes_simulator/internal/media/rtp"
)

type Sender struct {
	conn   *net.UDPConn
	addr   *net.UDPAddr
	owns   bool
	closed atomic.Bool
}

func New(broadcastAddr string, mediaPort int) (*Sender, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcastAddr, mediaPort))
	if err != nil {
		return nil, fmt.Errorf("resolve udp addr %s:%d: %w", broadcastAddr, mediaPort, err)
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s:%d: %w", broadcastAddr, mediaPort, err)
	}
	return &Sender{conn: conn, addr: addr, owns: true}, nil
}

func NewTo(addr *net.UDPAddr) (*Sender, error) {
	if addr == nil {
		return nil, fmt.Errorf("nil remote addr")
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s: %w", addr.String(), err)
	}
	return &Sender{conn: conn, addr: addr, owns: true}, nil
}

func NewFromConn(conn *net.UDPConn, addr *net.UDPAddr) (*Sender, error) {
	if conn == nil {
		return nil, fmt.Errorf("nil conn")
	}
	if addr == nil {
		return nil, fmt.Errorf("nil remote addr")
	}
	return &Sender{conn: conn, addr: addr, owns: false}, nil
}

func (s *Sender) Close() error {
	s.closed.Store(true)
	if !s.owns {
		return nil
	}
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("udp close: %w", err)
	}
	return nil
}

func (s *Sender) write(raw []byte) (int, error) {
	// Если conn создан через DialUDP, он "connected" и WriteTo* на Linux вернёт ошибку.
	if s.conn.RemoteAddr() != nil {
		n, err := s.conn.Write(raw)
		if err != nil {
			// При гонках закрытия shared-conn можем получить EBADF вместо net.ErrClosed.
			// Нормализуем в net.ErrClosed, чтобы callers могли стабильно проверять errors.Is(err, net.ErrClosed).
			if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EBADF) {
				return n, net.ErrClosed
			}
			return n, fmt.Errorf("udp write: %w", err)
		}
		return n, nil
	}
	n, err := s.conn.WriteToUDP(raw, s.addr)
	if err != nil {
		if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EBADF) {
			return n, net.ErrClosed
		}
		return n, fmt.Errorf("udp write to %s: %w", s.addr.String(), err)
	}
	return n, nil
}

func (s *Sender) StreamAt(ctx context.Context, t0 int64, pcm []int16) error {
	if len(pcm) == 0 {
		return nil
	}
	start := time.UnixMilli(t0)
	wait := time.Until(start)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		case <-timer.C:
		}
	}

	encState := &g726.G726EncoderState{}
	seq := mediarpt.RandomSequence()
	ssrc := mediarpt.RandomSSRC()
	rtpTS := uint32(0)

	frameTime := start
	for i := 0; i < len(pcm); i += mediarpt.SamplesPerFrame {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		default:
		}

		if d := time.Until(frameTime); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				if !t.Stop() {
					<-t.C
				}
				return fmt.Errorf("context done: %w", ctx.Err())
			case <-t.C:
			}
		}

		end := i + mediarpt.SamplesPerFrame
		frame := make([]int16, mediarpt.SamplesPerFrame)
		if end <= len(pcm) {
			copy(frame, pcm[i:end])
		} else {
			copy(frame, pcm[i:])
		}

		payload := g726.G726EncodeFrame(frame, encState)
		pkt := mediarpt.NewPacket(seq, rtpTS, ssrc, payload)
		raw, err := pkt.Marshal()
		if err != nil {
			return fmt.Errorf("rtp marshal: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context done: %w", err)
		}
		if s.closed.Load() {
			return net.ErrClosed
		}
		if _, err := s.write(raw); err != nil {
			if ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("context done: %w", ctx.Err())
			}
			return fmt.Errorf("udp write: %w", err)
		}

		seq++
		rtpTS += mediarpt.SamplesPerFrame
		frameTime = frameTime.Add(mediarpt.FrameDuration)
	}
	return nil
}

func (s *Sender) StreamFramesAt(ctx context.Context, t0 int64, frames <-chan []int16) error {
	start := time.UnixMilli(t0)
	wait := time.Until(start)
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		case <-timer.C:
		}
	}

	encState := &g726.G726EncoderState{}
	seq := mediarpt.RandomSequence()
	ssrc := mediarpt.RandomSSRC()
	rtpTS := uint32(0)
	silence := make([]int16, mediarpt.SamplesPerFrame)

	frameTime := start
	framesCh := frames
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		default:
		}

		// Если мы отстали больше чем на один фрейм (GC pause, scheduler stall и т.п.),
		// не пытаемся "догонять" реальное время burst-ом пакетов.
		// Вместо этого пропускаем интервалы, но сохраняем корректный ход RTP timestamp.
		if now := time.Now(); now.After(frameTime) {
			missed := now.Sub(frameTime) / mediarpt.FrameDuration
			if missed > 0 {
				rtpTS += uint32(missed) * mediarpt.SamplesPerFrame
				frameTime = frameTime.Add(missed * mediarpt.FrameDuration)
			}
		}

		if d := time.Until(frameTime); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				if !t.Stop() {
					<-t.C
				}
				return fmt.Errorf("context done: %w", ctx.Err())
			case <-t.C:
			}
		}

		frame := silence
		if framesCh != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context done: %w", ctx.Err())
			case f, ok := <-framesCh:
				if !ok {
					return nil
				}
				if f == nil {
					return nil
				}
				frame = f
			default:
			}
		}

		if len(frame) < mediarpt.SamplesPerFrame {
			padded := make([]int16, mediarpt.SamplesPerFrame)
			copy(padded, frame)
			frame = padded
		} else if len(frame) > mediarpt.SamplesPerFrame {
			frame = frame[:mediarpt.SamplesPerFrame]
		}

		payload := g726.G726EncodeFrame(frame, encState)
		pkt := mediarpt.NewPacket(seq, rtpTS, ssrc, payload)
		raw, err := pkt.Marshal()
		if err != nil {
			return fmt.Errorf("rtp marshal: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context done: %w", err)
		}
		if s.closed.Load() {
			return net.ErrClosed
		}
		if _, err := s.write(raw); err != nil {
			if ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("context done: %w", ctx.Err())
			}
			return fmt.Errorf("udp write: %w", err)
		}

		seq++
		rtpTS += mediarpt.SamplesPerFrame
		frameTime = frameTime.Add(mediarpt.FrameDuration)
	}
}

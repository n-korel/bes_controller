package sender

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"bucis-bes_simulator/internal/media/g726"
	mediarpt "bucis-bes_simulator/internal/media/rtp"
)

type Sender struct {
	conn   *net.UDPConn
	addr   *net.UDPAddr
	closed atomic.Bool
}

func New(broadcastAddr string, mediaPort int) (*Sender, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcastAddr, mediaPort))
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, addr: addr}, nil
}

func NewTo(addr *net.UDPAddr) (*Sender, error) {
	if addr == nil {
		return nil, fmt.Errorf("nil remote addr")
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, err
	}
	return &Sender{conn: conn, addr: addr}, nil
}

func (s *Sender) Close() error {
	s.closed.Store(true)
	return s.conn.Close()
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
			return ctx.Err()
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
			return ctx.Err()
		default:
		}

		if d := time.Until(frameTime); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				if !t.Stop() {
					<-t.C
				}
				return ctx.Err()
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
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.closed.Load() {
			return net.ErrClosed
		}
		if _, err := s.conn.Write(raw); err != nil {
			if ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return err
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
			return ctx.Err()
		case <-timer.C:
		}
	}

	encState := &g726.G726EncoderState{}
	seq := mediarpt.RandomSequence()
	ssrc := mediarpt.RandomSSRC()
	rtpTS := uint32(0)

	frameTime := start
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d := time.Until(frameTime); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				if !t.Stop() {
					<-t.C
				}
				return ctx.Err()
			case <-t.C:
			}
		}

		var frame []int16
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame = <-frames:
		}
		if frame == nil {
			return nil
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
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.closed.Load() {
			return net.ErrClosed
		}
		if _, err := s.conn.Write(raw); err != nil {
			if ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return err
		}

		seq++
		rtpTS += mediarpt.SamplesPerFrame
		frameTime = frameTime.Add(mediarpt.FrameDuration)
	}
}

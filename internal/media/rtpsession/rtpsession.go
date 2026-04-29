package rtpsession

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"

	"bucis-bes_simulator/internal/audio"
	"bucis-bes_simulator/internal/media/receiver"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/sender"
)

type Logger interface {
	Info(string, ...any)
	Warn(string, ...any)
}

// Start запускает RTP receiver + (опциональный) playback playout + sender streaming.
//
// Возвращаемая cleanup() останавливает receiver, закрывает sender и playback.
// Cancel контекста (ctx.Done) этим методом не производится: caller должен отменять/завершать ctx сам.
//
// В случае ошибки при создании TX receiver может быть уже запущен: cleanup будет ненулевым,
// а err вернёт причину. Caller может решить, возвращаться ли или продолжать.
func Start(
	ctx context.Context,
	logger Logger,
	localPort int,
	remoteAddr *net.UDPAddr,
) (cleanup func(), streamDone <-chan struct{}, err error) {
	rx := receiver.New(localPort)

	var pb *audio.Playback
	var tx *sender.Sender

	// 1) Playback playout (опционально)
	if audio.Enabled() {
		var startPBErr error
		pb, startPBErr = audio.StartPlayback(ctx, audio.Device())
		if startPBErr == nil {
			playout := make(chan []int16, 64)
			rx.SetPCMSink(func(pcm []int16) {
				select {
				case playout <- pcm:
				default:
				}
			})
			go func() {
				t := time.NewTicker(rtp.FrameDuration)
				defer t.Stop()

				silence := make([]int16, rtp.SamplesPerFrame)
				var last []int16
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						// Берём самый свежий кадр (сбрасывая накопившиеся), чтобы не раздувать задержку.
						for {
							select {
							case f := <-playout:
								last = f
							default:
								goto play
							}
						}
					play:
						if last == nil || len(last) != rtp.SamplesPerFrame {
							_ = pb.WritePCM(silence)
							continue
						}
						_ = pb.WritePCM(last)
					}
				}
			}()
		} else {
			logger.Warn("audio playback disabled", "err", startPBErr)
			pb = nil
		}
	}

	// 2) RX start with retry
	var startErr error
	for attempt := 1; attempt <= 10; attempt++ {
		startErr = rx.Start()
		if startErr == nil {
			break
		}
		if errors.Is(startErr, syscall.EADDRINUSE) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		break
	}
	if startErr != nil {
		logger.Warn("rtp rx start failed", "local_port", localPort, "err", startErr)
		if pb != nil {
			_ = pb.Close()
		}
		return nil, nil, startErr
	}
	logger.Info("rtp rx started", "local_port", rx.MediaPort())

	var cleanupOnce sync.Once
	cleanup = func() {
		cleanupOnce.Do(func() {
			// Сначала останавливаем исходящий поток (tx), потом принимающий (rx),
			// и только в конце закрываем playback.
			if tx != nil {
				_ = tx.Close()
			}
			if rx != nil {
				stats, stopErr := rx.Stop()
				if stopErr != nil {
					logger.Warn("rtp rx stop failed", "err", stopErr)
				}
				if stats.Received > 0 {
					logger.Info("rtp stats",
						"received", stats.Received,
						"expected", stats.Expected(),
						"lost", stats.Lost(),
						"jitter_ms", stats.JitterMs(),
					)
				}
			}
			if pb != nil {
				_ = pb.Close()
			}
		})
	}

	// 3) RX health loop (для логов)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s := rx.StatsSnapshot()
				age := time.Since(s.LastPacket)
				if s.Received == 0 {
					logger.Info("rtp health", "received", 0, "last_packet_age", age.String())
					continue
				}
				logger.Info("rtp health",
					"received", s.Received,
					"expected", s.Expected(),
					"lost", s.Lost(),
					"jitter_ms", s.JitterMs(),
					"last_packet_age", age.String(),
				)
			}
		}
	}()

	// 4) TX create
	tx, err = sender.NewTo(remoteAddr)
	if err != nil {
		if c := rx.Conn(); c != nil {
			tx, err = sender.NewFromConn(c, remoteAddr)
		}
	}
	if err != nil {
		logger.Warn("rtp tx create failed", "remote_ip", remoteAddr.IP.String(), "remote_port", remoteAddr.Port, "err", err)
		return cleanup, nil, err
	}
	logger.Info("rtp tx started", "remote_ip", remoteAddr.IP.String(), "remote_port", remoteAddr.Port)

	// 5) Capture frames (или silence generator)
	var frames <-chan []int16
	if audio.Enabled() {
		ch, capErr := audio.StartCaptureFrames(ctx, audio.Device())
		if capErr == nil {
			frames = ch
		} else {
			logger.Warn("audio capture disabled", "err", capErr)
		}
	}
	if frames == nil {
		silenceCh := make(chan []int16, 1)
		frames = silenceCh
		go func() {
			ticker := time.NewTicker(rtp.FrameDuration)
			defer ticker.Stop()

			silence := make([]int16, rtp.SamplesPerFrame)
			for {
				select {
				case <-ctx.Done():
					close(silenceCh)
					return
				case <-ticker.C:
					select {
					case silenceCh <- silence:
					default:
					}
				}
			}
		}()
	}

	// 6) Streaming loop
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		if streamErr := tx.StreamFramesAt(ctx, time.Now().UnixMilli(), frames); streamErr != nil && ctx.Err() == nil {
			logger.Warn("rtp tx stopped with error", "err", streamErr)
		}
	}()

	return cleanup, doneCh, nil
}


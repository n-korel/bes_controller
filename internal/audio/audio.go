//go:build !windows

package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Playback struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu        sync.Mutex
	closeOnce sync.Once
}

func StartPlayback(ctx context.Context, device string) (*Playback, error) {
	if strings.TrimSpace(device) == "" {
		device = defaultALSACard
	}
	if device == "null" {
		return nil, errors.New("alsa playback disabled (ALSA_DEVICE=null)")
	}

	args := []string{
		"-q",
		"-D", device,
		"-f", "S16_LE",
		"-c", "1",
		"-r", "8000",
		"-t", "raw",
	}

	// По умолчанию даём aplay чуть больший буфер, чтобы меньше ловить underrun
	// из-за планировщика/GC. Можно переопределить через env или выключить (0).
	if v := strings.TrimSpace(os.Getenv("AUDIO_APLAY_BUFFER_US")); v != "" {
		if us, err := strconv.Atoi(v); err == nil {
			if us > 0 {
				args = append(args, "-B", strconv.Itoa(us))
			}
		}
	} else {
		args = append(args, "-B", "200000")
	}

	cmd := exec.CommandContext(ctx, "aplay", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("aplay stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("aplay start: %w", err)
	}

	p := &Playback{cmd: cmd, stdin: stdin}
	go func() {
		<-ctx.Done()
		_ = stdin.Close()
	}()
	return p, nil
}

func (p *Playback) WritePCM(pcm []int16) error {
	if p == nil || p.stdin == nil || len(pcm) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return nil
	}
	n := len(pcm) * 2
	buf := make([]byte, n)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	_, err := p.stdin.Write(buf)
	if err != nil {
		return fmt.Errorf("aplay write: %w", err)
	}
	return nil
}

func (p *Playback) Close() error {
	if p == nil {
		return nil
	}
	var err error
	p.closeOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.cmd == nil {
			return
		}

		waitDone := make(chan error, 1)
		go func() { waitDone <- p.cmd.Wait() }()

		select {
		case werr := <-waitDone:
			if werr != nil {
				err = fmt.Errorf("aplay wait: %w", werr)
			}
		case <-time.After(2 * time.Second):
			// Если внешний проигрыватель завис/не завершается после EOF,
			// не даём Close() блокировать cleanup навсегда.
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			werr := <-waitDone
			if werr != nil {
				err = fmt.Errorf("aplay wait after kill: %w", werr)
			}
		}
	})
	return err
}

func StartCaptureFrames(ctx context.Context, device string) (<-chan []int16, error) {
	if strings.TrimSpace(device) == "" {
		device = defaultALSACard
	}
	if device == "null" {
		return nil, errors.New("alsa capture disabled (ALSA_DEVICE=null)")
	}

	cmd := exec.CommandContext(ctx, "arecord",
		"-q",
		"-D", device,
		"-f", "S16_LE",
		"-c", "1",
		"-r", "8000",
		"-t", "raw",
	)
	cmd.Stdin = nil
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("arecord stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("arecord start: %w", err)
	}

	out := make(chan []int16, 4)
	go func() {
		defer close(out)
		defer func() { _ = cmd.Wait() }()

		buf := make([]byte, frameBytes)
		for {
			_, err := io.ReadFull(stdout, buf)
			if err != nil {
				return
			}
			frame := make([]int16, SamplesPerFrame)
			for i := 0; i < SamplesPerFrame; i++ {
				frame[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
			}
			select {
			case out <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

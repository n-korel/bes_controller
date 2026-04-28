//go:build !windows

package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Playback struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func StartPlayback(ctx context.Context, device string) (*Playback, error) {
	if strings.TrimSpace(device) == "" {
		device = defaultALSACard
	}
	if device == "null" {
		return nil, errors.New("alsa playback disabled (ALSA_DEVICE=null)")
	}

	cmd := exec.CommandContext(ctx, "aplay",
		"-q",
		"-D", device,
		"-f", "S16_LE",
		"-c", "1",
		"-r", "8000",
		"-t", "raw",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
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
	buf := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	_, err := p.stdin.Write(buf)
	return err
}

func (p *Playback) Close() error {
	if p == nil {
		return nil
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
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
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
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

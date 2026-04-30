//go:build windows

package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Windows implementation uses external tools:
// - playback: ffplay reads raw S16LE PCM from stdin
// - capture:  ffmpeg captures from DirectShow device and writes raw S16LE PCM to stdout
//
// This keeps the build cgo-free and matches the existing "spawn a tool" model used on Linux.

type Playback struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	closeOnce sync.Once
}

func StartPlayback(ctx context.Context, device string) (*Playback, error) {
	// "device" is ignored for playback; ffplay uses default system output.
	if strings.TrimSpace(device) == "null" {
		return nil, errors.New("audio playback disabled (ALSA_DEVICE=null)")
	}

	cmd := exec.CommandContext(ctx, "ffplay",
		"-nodisp",
		"-autoexit",
		"-loglevel", "error",
		"-f", "s16le",
		"-ar", "8000",
		"-af", "aformat=sample_fmts=s16:sample_rates=8000:channel_layouts=mono",
		"-i", "pipe:0",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if strings.TrimSpace(os.Getenv("AUDIO_DEBUG")) == "1" {
		fmt.Fprintf(os.Stderr, "audio: starting %q args=%q\n", cmd.Path, cmd.Args)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start ffplay failed: %w (install ffmpeg and ensure ffplay is in PATH)", err)
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
		case err = <-waitDone:
		case <-time.After(2 * time.Second):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			err = <-waitDone
		}
	})
	return err
}

func StartCaptureFrames(ctx context.Context, device string) (<-chan []int16, error) {
	dev := strings.TrimSpace(device)
	if dev == "" || dev == defaultALSACard {
		return nil, errors.New(`audio capture device not set on Windows: set ALSA_DEVICE to the DirectShow microphone name (see: ffmpeg -list_devices true -f dshow -i dummy)`)
	}
	if dev == "null" {
		return nil, errors.New("audio capture disabled (ALSA_DEVICE=null)")
	}

	// DirectShow input.
	// Example device value: Microphone (Realtek(R) Audio)
	input := fmt.Sprintf(`audio=%s`, dev)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "dshow",
		"-i", input,
		"-af", "aformat=sample_fmts=s16:sample_rates=8000:channel_layouts=mono",
		"-f", "s16le",
		"pipe:1",
	)
	cmd.Stdin = nil
	cmd.Stderr = os.Stderr
	if strings.TrimSpace(os.Getenv("AUDIO_DEBUG")) == "1" {
		fmt.Fprintf(os.Stderr, "audio: starting %q args=%q\n", cmd.Path, cmd.Args)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg failed: %w (install ffmpeg and ensure ffmpeg is in PATH)", err)
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

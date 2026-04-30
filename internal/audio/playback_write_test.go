//go:build !windows

package audio

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
)

type bufWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (b *bufWriteCloser) Close() error {
	b.closed = true
	return nil
}

func TestPlayback_WritePCM_WritesLittleEndian(t *testing.T) {
	wc := &bufWriteCloser{}
	p := &Playback{stdin: wc}

	if err := p.WritePCM([]int16{0x0102, -1}); err != nil {
		t.Fatalf("WritePCM: %v", err)
	}
	got := wc.Bytes()
	want := []byte{0x02, 0x01, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes=%v want %v", got, want)
	}
}

func TestPlayback_Close_ClosesStdin_WhenNoCmd(t *testing.T) {
	wc := &bufWriteCloser{}
	p := &Playback{stdin: wc}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !wc.closed {
		t.Fatalf("expected stdin to be closed")
	}
}

func TestPlayback_Close_WaitsForCmd_Success(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	p := &Playback{cmd: cmd}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPlayback_Close_WaitsForCmd_Error(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 7")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	p := &Playback{cmd: cmd}
	if err := p.Close(); err == nil {
		t.Fatalf("expected error")
	}
}

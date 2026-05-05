//go:build !windows

package audio

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
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

type syncBufWriteCloser struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBufWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// bytes.Buffer.Write по контракту всегда возвращает nil error, но wrapcheck
	// не любит "пробрасывание" ошибок из внешних пакетов — поэтому не возвращаем её наружу.
	n, _ := w.b.Write(p)
	return n, nil
}

func (w *syncBufWriteCloser) Close() error { return nil }

func (w *syncBufWriteCloser) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Len()
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

func TestPlayback_WritePCM_Concurrent(t *testing.T) {
	wc := &syncBufWriteCloser{}
	p := &Playback{stdin: wc}

	const (
		goroutines = 8
		iterations = 300
		samples    = 160
	)

	frame := make([]int16, samples)
	for i := range frame {
		frame[i] = int16(i * 7)
	}

	var expectedBytes int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if err := p.WritePCM(frame); err != nil {
					t.Errorf("WritePCM: %v", err)
					return
				}
				atomic.AddInt64(&expectedBytes, int64(len(frame)*2))
			}
		}()
	}
	close(start)
	wg.Wait()

	if got, want := int64(wc.Len()), atomic.LoadInt64(&expectedBytes); got != want {
		t.Fatalf("bytes written=%d want %d", got, want)
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

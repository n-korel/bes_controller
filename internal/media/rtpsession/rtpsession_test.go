package rtpsession

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bucis-bes_simulator/internal/media/receiver"
	"bucis-bes_simulator/internal/media/rtp"
	"bucis-bes_simulator/internal/media/sender"

	"go.uber.org/goleak"
)

func TestRTPSession_TXAfterRXStop_ReturnsErrClosed(t *testing.T) {
	rx := receiver.New(0)
	if err := rx.Start(); err != nil {
		t.Fatalf("rx.Start(): %v", err)
	}
	conn := rx.Conn()
	if conn == nil {
		t.Fatalf("rx.Conn() is nil")
	}

	remote, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(remote): %v", err)
	}
	defer func() { _ = remote.Close() }()

	remoteAddr, ok := remote.LocalAddr().(*net.UDPAddr)
	if !ok || remoteAddr == nil {
		t.Fatalf("remote.LocalAddr(): %T", remote.LocalAddr())
	}

	tx, err := sender.NewFromConn(conn, remoteAddr)
	if err != nil {
		t.Fatalf("sender.NewFromConn(): %v", err)
	}
	defer func() { _ = tx.Close() }()

	if _, err := rx.Stop(); err != nil {
		t.Fatalf("rx.Stop(): %v", err)
	}

	pcm := make([]int16, rtp.SamplesPerFrame)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err = tx.StreamAt(ctx, time.Now().UnixMilli(), pcm)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("err=%v; want %v", err, net.ErrClosed)
	}
}

type testLogger struct{}

func (testLogger) Info(string, ...any) {}
func (testLogger) Warn(string, ...any) {}

func TestRTPSession_Start_NilRemoteAddr_ReturnsError(t *testing.T) {
	t.Setenv("NO_AUDIO", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleanup, streamDone, err := Start(ctx, testLogger{}, 0, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if cleanup != nil {
		t.Fatalf("cleanup should be nil on error, got non-nil")
	}
	if streamDone != nil {
		t.Fatalf("streamDone should be nil on error, got non-nil")
	}
}

func TestRTPSession_Cleanup_IdempotentAndClosesStream(t *testing.T) {
	t.Setenv("NO_AUDIO", "1")

	remote, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(remote): %v", err)
	}
	defer func() { _ = remote.Close() }()

	remoteAddr, ok := remote.LocalAddr().(*net.UDPAddr)
	if !ok || remoteAddr == nil {
		t.Fatalf("remote.LocalAddr(): %T", remote.LocalAddr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleanup, streamDone, err := Start(ctx, testLogger{}, 0, remoteAddr)
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if cleanup == nil {
		t.Fatalf("cleanup is nil")
	}
	if streamDone == nil {
		t.Fatalf("streamDone is nil")
	}

	cleanup()
	cleanup()

	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("streamDone did not close after cleanup")
	}
}

func TestRTPSession_PlayoutGoroutine_NoLeakOnCtxCancel(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Включаем audio-ветку, но подменяем внешние процессы на фейки.
	t.Setenv("NO_AUDIO", "")
	t.Setenv("ALSA_DEVICE", "default")
	t.Setenv("AUDIO_PLAYOUT_PREFILL_FRAMES", "0")

	tmp := t.TempDir()
	mustWriteExe(t, filepath.Join(tmp, "aplay"), "#!/usr/bin/env bash\ncat >/dev/null\n")
	mustWriteExe(t, filepath.Join(tmp, "arecord"), "#!/usr/bin/env bash\nexit 1\n")
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	remote, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(remote): %v", err)
	}
	defer func() { _ = remote.Close() }()

	remoteAddr, ok := remote.LocalAddr().(*net.UDPAddr)
	if !ok || remoteAddr == nil {
		t.Fatalf("remote.LocalAddr(): %T", remote.LocalAddr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, streamDone, err := Start(ctx, testLogger{}, 0, remoteAddr)
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	cancel()

	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("streamDone did not close after ctx cancel")
	}
}

func mustWriteExe(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

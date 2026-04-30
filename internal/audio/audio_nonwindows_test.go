//go:build !windows

package audio

import (
	"context"
	"strings"
	"testing"
)

func TestStartPlayback_DeviceNullReturnsError(t *testing.T) {
	_, err := StartPlayback(context.Background(), "null")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartCaptureFrames_DeviceNullReturnsError(t *testing.T) {
	_, err := StartCaptureFrames(context.Background(), "null")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlayback_WritePCM_NoOps(t *testing.T) {
	var p *Playback
	if err := p.WritePCM([]int16{1, 2}); err != nil {
		t.Fatalf("WritePCM(nil): %v", err)
	}

	p = &Playback{}
	if err := p.WritePCM(nil); err != nil {
		t.Fatalf("WritePCM(nil slice): %v", err)
	}
	if err := p.WritePCM([]int16{}); err != nil {
		t.Fatalf("WritePCM(empty): %v", err)
	}
	if err := p.WritePCM([]int16{1, 2}); err != nil {
		t.Fatalf("WritePCM(no stdin): %v", err)
	}
}

func TestPlayback_Close_NoOps(t *testing.T) {
	var p *Playback
	if err := p.Close(); err != nil {
		t.Fatalf("Close(nil): %v", err)
	}

	p = &Playback{}
	if err := p.Close(); err != nil {
		t.Fatalf("Close(empty): %v", err)
	}
	// Повторный Close тоже должен быть безопасен.
	if err := p.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}
}

func TestStartPlayback_CommandNotFound_ReturnsError(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := StartPlayback(context.Background(), "default")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "aplay start") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartCaptureFrames_CommandNotFound_ReturnsError(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := StartCaptureFrames(context.Background(), "default")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "arecord start") {
		t.Fatalf("unexpected error: %v", err)
	}
}

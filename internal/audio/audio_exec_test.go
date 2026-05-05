//go:build !windows

package audio

import (
	"context"
	"testing"
)

func TestStartPlayback_NullDevice_ReturnsError(t *testing.T) {
	_, err := StartPlayback(context.Background(), "null")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestStartCaptureFrames_NullDevice_ReturnsError(t *testing.T) {
	_, err := StartCaptureFrames(context.Background(), "null")
	if err == nil {
		t.Fatalf("expected error")
	}
}

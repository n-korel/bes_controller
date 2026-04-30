package bes

import (
	"context"
	"strings"
	"testing"
	"time"

	"bucis-bes_simulator/internal/infra/config"
)

func TestDoPress_CancelCh(t *testing.T) {
	cfg := config.Bes{
		EC: config.EC{
			BucisAddr:                "127.0.0.1",
			BesQueryDstPort:          6710,
			ClientQueryMaxRetries:    1,
			ClientAnswerTimeout:      2 * time.Second,
			ClientQueryRetryInterval: 10 * time.Millisecond,
		},
	}

	answers := make(chan answerEvent, 1)
	cancelCh := make(chan struct{})
	close(cancelCh)

	_, _, err := doPress(
		context.Background(),
		nopLogger{},
		cfg,
		func() error { return nil },
		answers,
		cancelCh,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

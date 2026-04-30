package bes

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

func TestDoPress_SucceedsAfterRetry(t *testing.T) {
	var sendCalls atomic.Int32
	answers := make(chan answerEvent, 8)

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      40 * time.Millisecond,
		ClientQueryRetryInterval: 10 * time.Millisecond,
		ClientQueryMaxRetries:    3,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sendQueryOnce := func() error {
		n := sendCalls.Add(1)
		if n == 2 {
			go func() {
				time.Sleep(5 * time.Millisecond)
				answers <- answerEvent{answer: &protocol.ClientAnswer{NewIP: "1.2.3.4", SipID: "42"}}
			}()
		}
		return nil
	}

	sipID, newIP, err := doPress(ctx, nopLogger{}, cfg, sendQueryOnce, answers)
	if err != nil {
		t.Fatalf("doPress error: %v", err)
	}
	if sipID != "42" || newIP != "1.2.3.4" {
		t.Fatalf("got sipID=%q newIP=%q", sipID, newIP)
	}
	if sendCalls.Load() < 2 {
		t.Fatalf("expected at least 2 send attempts, got %d", sendCalls.Load())
	}
}

func TestDoPress_StopsOnContextCancel(t *testing.T) {
	answers := make(chan answerEvent, 8)
	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      200 * time.Millisecond,
		ClientQueryRetryInterval: 200 * time.Millisecond,
		ClientQueryMaxRetries:    10,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := doPress(ctx, nopLogger{}, cfg, func() error { return nil }, answers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDoPress_FailsAfterMaxRetries(t *testing.T) {
	answers := make(chan answerEvent, 8)
	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      10 * time.Millisecond,
		ClientQueryRetryInterval: 5 * time.Millisecond,
		ClientQueryMaxRetries:    3,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := doPress(ctx, nopLogger{}, cfg, func() error { return nil }, answers)
	if err == nil {
		t.Fatalf("expected error")
	}
}


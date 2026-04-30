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

	sipID, newIP, err := doPress(ctx, nopLogger{}, cfg, sendQueryOnce, answers, nil)
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

	_, _, err := doPress(ctx, nopLogger{}, cfg, func() error { return nil }, answers, nil)
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

	_, _, err := doPress(ctx, nopLogger{}, cfg, func() error { return nil }, answers, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestDoPress_AnswerDuringRetry(t *testing.T) {
	var sendCalls atomic.Int32
	answers := make(chan answerEvent, 8)

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      20 * time.Millisecond,
		ClientQueryRetryInterval: 120 * time.Millisecond,
		ClientQueryMaxRetries:    3,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	sendQueryOnce := func() error {
		n := sendCalls.Add(1)
		if n == 1 {
			// Ответ приходит уже после timeout первой попытки,
			// но во время ожидания retryInterval (между ретраями).
			go func() {
				time.Sleep(40 * time.Millisecond)
				answers <- answerEvent{answer: &protocol.ClientAnswer{NewIP: "10.0.0.1", SipID: "77"}}
			}()
		}
		return nil
	}

	sipID, newIP, err := doPress(ctx, nopLogger{}, cfg, sendQueryOnce, answers, nil)
	if err != nil {
		t.Fatalf("doPress error: %v", err)
	}
	if sipID != "77" || newIP != "10.0.0.1" {
		t.Fatalf("got sipID=%q newIP=%q", sipID, newIP)
	}
	if got := sendCalls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 send attempts, got %d", got)
	}
	if d := time.Since(start); d < cfg.EC.ClientQueryRetryInterval {
		t.Fatalf("expected to wait at least retryInterval, elapsed=%v retryInterval=%v", d, cfg.EC.ClientQueryRetryInterval)
	}
	if d := time.Since(start); d > cfg.EC.ClientQueryRetryInterval+cfg.EC.ClientAnswerTimeout+150*time.Millisecond {
		t.Fatalf("unexpectedly slow: elapsed=%v", d)
	}
}

func TestDoPress_CancelCh_DuringRetryInterval(t *testing.T) {
	var sendCalls atomic.Int32
	answers := make(chan answerEvent, 8)
	cancelCh := make(chan struct{})

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      15 * time.Millisecond,
		ClientQueryRetryInterval: 200 * time.Millisecond,
		ClientQueryMaxRetries:    10,
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sendFirst := make(chan struct{}, 1)
	sendQueryOnce := func() error {
		n := sendCalls.Add(1)
		if n == 1 {
			sendFirst <- struct{}{}
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := doPress(ctx, nopLogger{}, cfg, sendQueryOnce, answers, cancelCh)
		done <- err
	}()

	select {
	case <-sendFirst:
	case <-ctx.Done():
		t.Fatalf("timeout waiting for first send")
	}

	// Даем первой попытке гарантированно уйти в timeout,
	// и попадаем в интервал ожидания между ретраями.
	time.Sleep(cfg.EC.ClientAnswerTimeout + 10*time.Millisecond)
	close(cancelCh)

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for doPress to stop after cancel")
	}

	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 send attempt, got %d", got)
	}
}

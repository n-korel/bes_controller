package bes

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

type debugNopLogger struct{}

func (debugNopLogger) Info(string, ...any)  {}
func (debugNopLogger) Warn(string, ...any)  {}
func (debugNopLogger) Debug(string, ...any) {}

func TestRun_ReceivesPackets_AndFiresCallbacks(t *testing.T) {
	cfg := config.Bes{
		EC: config.EC{
			ListenPort8890:           0,
			BucisAddr:                "127.0.0.1",
			BesQueryDstPort:          6710,
			ClientQueryMaxRetries:    1,
			ClientAnswerTimeout:      50 * time.Millisecond,
			ClientQueryRetryInterval: 10 * time.Millisecond,
			KeepAliveTimeout:         0, // disable keepalive watchdog goroutine
		},
		MAC: "AA:BB:CC:DD:EE:FF",
	}

	resetCh := make(chan struct{}, 8)
	answerCh := make(chan *protocol.ClientAnswer, 8)
	convCh := make(chan string, 8)
	listenPortCh := make(chan int, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, debugNopLogger{}, cfg, Options{
			MAC:                    cfg.MAC,
			OnListenPort:           listenPortCh,
			OnResetReceived:        resetCh,
			OnAnswerReceived:       answerCh,
			OnConversationReceived: convCh,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	var port int
	select {
	case port = <-listenPortCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for listen port")
	}

	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	sender, err := net.DialUDP("udp4", nil, dst)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = sender.Close() }()

	send := func(payload string) {
		t.Helper()
		_ = sender.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := sender.Write([]byte(payload)); err != nil {
			t.Fatalf("udp write: %v", err)
		}
	}

	// 1) Reset -> callback
	send(protocol.FormatClientReset("1.1.1.1", "2.2.2.2"))
	select {
	case <-resetCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for reset callback on :%d", port)
	}

	// 2) KeepAlive -> no callback, but покрываем ветку (debug logger implements Debug).
	send(protocol.FormatKeepAlive("9.9.9.9", "0"))

	// 3) Conversation -> callback
	send(protocol.FormatClientConversation("123"))
	select {
	case got := <-convCh:
		if got != "123" {
			t.Fatalf("conversation=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for conversation callback on :%d", port)
	}

	// 4) Answer -> callback
	send(protocol.FormatClientAnswer("1.2.3.4", "777"))
	select {
	case got := <-answerCh:
		if got == nil || got.NewIP != "1.2.3.4" || got.SipID != "777" {
			t.Fatalf("answer=%v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for answer callback on :%d", port)
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			return
		}
		// По контракту Run возвращает ошибку при ctx.Done(); допускаем.
		if ctx.Err() == nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run did not stop after context cancel (port=%s)", strconv.Itoa(port))
	}
}

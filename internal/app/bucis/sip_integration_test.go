//go:build integration
// +build integration

package bucis

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

type logEvent struct {
	level string
	msg   string
}

type captureLogger struct {
	ch chan logEvent
}

func newCaptureLogger() *captureLogger {
	return &captureLogger{ch: make(chan logEvent, 128)}
}

func (l *captureLogger) Info(msg string, _ ...any)  { l.ch <- logEvent{level: "info", msg: msg} }
func (l *captureLogger) Warn(msg string, _ ...any)  { l.ch <- logEvent{level: "warn", msg: msg} }
func (l *captureLogger) Debug(msg string, _ ...any) { l.ch <- logEvent{level: "debug", msg: msg} }

func TestIntegrationSIP_RegisterOK_ThenCancel(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("set TEST_INTEGRATION to run integration tests")
	}

	port := mustReserveUDPPort(t)
	hostPort := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("sipgo.NewUA(): %v", err)
	}
	defer func() { _ = ua.Close() }()

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatalf("sipgo.NewServer(): %v", err)
	}

	// Minimal UAS: answer 200 OK to REGISTER, no auth challenge.
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		tx.Respond(res)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srvReady := make(chan struct{})
	go srv.ListenAndServe(
		context.WithValue(ctx, sipgo.ListenReadyCtxKey, sipgo.ListenReadyCtxValue(srvReady)),
		"udp",
		hostPort,
	)
	<-srvReady
	time.Sleep(100 * time.Millisecond) // avoid UDP listener race on some systems

	t.Setenv("SIP_USER_BUCIS", "bucis")
	t.Setenv("SIP_PASS_BUCIS", "pass")
	t.Setenv("SIP_PORT", strconv.Itoa(port))
	t.Setenv("SIP_REGISTER_TIMEOUT", "1s")

	logger := newCaptureLogger()
	b := &BUCIS{
		logger:    logger,
		cfg:       nil,
		sipIDs:    newSipIDState(1000),
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo:    func(string, int, string) error { return nil },
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer initCancel()

	var wg sync.WaitGroup
	if ok := b.initSIP(initCtx, &wg); !ok {
		t.Fatalf("initSIP()=false; want true")
	}

	if err := waitLog(initCtx, logger.ch, func(e logEvent) bool {
		return e.level == "info" && strings.Contains(e.msg, "sip registered")
	}); err != nil {
		t.Fatalf("did not observe sip registered: %v", err)
	}

	initCancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("initSIP goroutine did not stop after cancel")
	}
}

func TestIntegrationSIP_RegisterRetry_ThenOK(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("set TEST_INTEGRATION to run integration tests")
	}

	port := mustReserveUDPPort(t)
	hostPort := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("sipgo.NewUA(): %v", err)
	}
	defer func() { _ = ua.Close() }()

	srv, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatalf("sipgo.NewServer(): %v", err)
	}

	var regCount int32
	var authSeen int32
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		atomic.AddInt32(&regCount, 1)

		// Challenge first REGISTER to force a second request (with Authorization).
		if req.GetHeader("authorization") == nil {
			res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
			res.AppendHeader(sip.NewHeader("WWW-Authenticate", `Digest realm="bucis-test", nonce="nonce", algorithm=MD5, qop="auth"`))
			tx.Respond(res)
			return
		}

		atomic.StoreInt32(&authSeen, 1)
		res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		tx.Respond(res)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srvReady := make(chan struct{})
	go srv.ListenAndServe(
		context.WithValue(ctx, sipgo.ListenReadyCtxKey, sipgo.ListenReadyCtxValue(srvReady)),
		"udp",
		hostPort,
	)
	<-srvReady
	time.Sleep(100 * time.Millisecond)

	t.Setenv("SIP_USER_BUCIS", "bucis")
	t.Setenv("SIP_PASS_BUCIS", "pass")
	t.Setenv("SIP_PORT", strconv.Itoa(port))
	t.Setenv("SIP_REGISTER_TIMEOUT", "2s")

	logger := newCaptureLogger()
	b := &BUCIS{
		logger:    logger,
		cfg:       nil,
		sipIDs:    newSipIDState(1000),
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo:    func(string, int, string) error { return nil },
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer initCancel()

	var wg sync.WaitGroup
	if ok := b.initSIP(initCtx, &wg); !ok {
		t.Fatalf("initSIP()=false; want true")
	}

	if err := waitLog(initCtx, logger.ch, func(e logEvent) bool {
		return e.level == "info" && strings.Contains(e.msg, "sip registered")
	}); err != nil {
		t.Fatalf("did not observe sip registered: %v", err)
	}

	if got := atomic.LoadInt32(&regCount); got < 2 {
		t.Fatalf("expected at least 2 REGISTER requests (challenge + auth), got %d", got)
	}
	if atomic.LoadInt32(&authSeen) != 1 {
		t.Fatalf("expected REGISTER with Authorization to be observed")
	}

	initCancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("initSIP goroutine did not stop after cancel")
	}
}

func waitLog(ctx context.Context, ch <-chan logEvent, pred func(logEvent) bool) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		case e := <-ch:
			if pred(e) {
				return nil
			}
		}
	}
}

func mustReserveUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	laddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || laddr == nil {
		t.Fatalf("LocalAddr: %T", conn.LocalAddr())
	}
	return laddr.Port
}

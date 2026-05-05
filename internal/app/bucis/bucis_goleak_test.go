package bucis

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"go.uber.org/goleak"
)

type goleakCaptureLogger struct {
	ch chan string
}

func newGoleakCaptureLogger() *goleakCaptureLogger {
	return &goleakCaptureLogger{ch: make(chan string, 256)}
}

func (l *goleakCaptureLogger) Info(msg string, _ ...any)  { l.ch <- "info:" + msg }
func (l *goleakCaptureLogger) Warn(msg string, _ ...any)  { l.ch <- "warn:" + msg }
func (l *goleakCaptureLogger) Debug(msg string, _ ...any) { l.ch <- "debug:" + msg }

func TestInitSIP_NoGoroutineLeakAfterMultipleCalls(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	port := mustReserveUDPPortForGoleak(t)
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
	srv.OnRegister(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
		_ = tx.Respond(res)
	})

	srvCtx, srvCancel := context.WithCancel(context.Background())
	var srvWG sync.WaitGroup
	srvWG.Add(1)
	srvReady := make(chan struct{})
	go func() {
		defer srvWG.Done()
		_ = srv.ListenAndServe(
			//nolint:staticcheck // sipgo expects this key in ctx for readiness notification.
			context.WithValue(srvCtx, sipgo.ListenReadyCtxKey, sipgo.ListenReadyCtxValue(srvReady)),
			"udp",
			hostPort,
		)
	}()
	<-srvReady
	time.Sleep(100 * time.Millisecond) // avoid UDP listener race on some systems

	t.Setenv("SIP_USER_BUCIS", "bucis")
	t.Setenv("SIP_PASS_BUCIS", "pass")
	t.Setenv("SIP_PORT", strconv.Itoa(port))
	t.Setenv("SIP_REGISTER_TIMEOUT", "1s")

	logger := newGoleakCaptureLogger()
	b := &BUCIS{
		logger:    logger,
		cfg:       nil,
		sipIDs:    newSipIDState(1000),
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo:    func(string, int, string) error { return nil },
	}

	for i := 0; i < 3; i++ {
		initCtx, initCancel := context.WithTimeout(context.Background(), 2*time.Second)

		var wg sync.WaitGroup
		if ok := b.initSIP(initCtx, &wg); !ok {
			initCancel()
			t.Fatalf("initSIP()=false; want true (iter=%d)", i)
		}

		if err := waitForRegisteredLog(initCtx, logger.ch); err != nil {
			initCancel()
			t.Fatalf("did not observe sip registered (iter=%d): %v", i, err)
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
			t.Fatalf("initSIP goroutine did not stop after cancel (iter=%d)", i)
		}
	}

	srvCancel()
	srvWG.Wait()
}

func waitForRegisteredLog(ctx context.Context, ch <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		case msg := <-ch:
			if strings.HasPrefix(msg, "info:") && strings.Contains(msg, "sip registered") {
				return nil
			}
		}
	}
}

func mustReserveUDPPortForGoleak(t *testing.T) int {
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

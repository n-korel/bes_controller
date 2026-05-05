package bucis

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtpsession"

	"github.com/emiago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
)

// Проверяет: при ошибке sendTo — phone.Answer не должен вызываться
// до завершения предыдущего диалога.
func TestInitSIP_SendToFailure_DoesNotAcceptConcurrentDialog(t *testing.T) {
	cfg := &config.Bucis{EC: config.EC{ListenPort8890: 8890}}
	sipIDs := newSipIDState(0)
	sipIDs.SetIP("123", "127.0.0.1")

	var sendCalls int32
	b := &BUCIS{
		logger:    nopLogger{},
		cfg:       cfg,
		sipIDs:    sipIDs,
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo: func(string, int, string) error {
			atomic.AddInt32(&sendCalls, 1)
			return errors.New("send failed")
		},
	}

	// Минимальный диалог с InviteRequest.From (для sipId) и "живым" Context().
	req := sip.NewRequest(sip.INVITE, sip.Uri{Host: "example.invalid"})
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{User: "bes_123", Host: "example.invalid"},
		Params:  sip.NewParams(),
	})
	req.SetSource("127.0.0.1:5060")

	ds := &sipgo.DialogServerSession{}
	ds.InviteRequest = req
	ds.Init()

	dialog := &sipgox.DialogServerSession{
		DialogServerSession: ds,
	}

	// Если handleAnsweredDialog вернётся сразу после sendTo-ошибки,
	// initSIP-петля сможет вызвать Answer повторно, пока диалог ещё активен.
	// Здесь ожидаем, что handleAnsweredDialog будет ждать завершения (ctx cancel / dialog.Context done).
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	exitAnswerLoop := b.handleAnsweredDialog(ctx, dialog)
	elapsed := time.Since(start)

	if atomic.LoadInt32(&sendCalls) != 1 {
		t.Fatalf("sendTo calls: got %d want %d", sendCalls, 1)
	}
	if !exitAnswerLoop {
		t.Fatalf("exitAnswerLoop: got false want true (ctx should end the wait)")
	}
	if elapsed < 120*time.Millisecond {
		t.Fatalf("handleAnsweredDialog returned too early: elapsed=%s; want it to block until ctx cancel", elapsed)
	}
}

// Ветка sipID найден, но besIP пустой — должны ждать dialog.Context().Done()
// и НЕ принимать новый вызов.
func TestInitSIP_EmptyBesIP_WaitsForDialogDone(t *testing.T) {
	cfg := &config.Bucis{EC: config.EC{ListenPort8890: 8890}}
	sipIDs := newSipIDState(0)
	sipIDs.SetIP("123", "") // sipID найден, но besIP пустой

	var sendCalls int32
	b := &BUCIS{
		logger:    nopLogger{},
		cfg:       cfg,
		sipIDs:    sipIDs,
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo: func(string, int, string) error {
			atomic.AddInt32(&sendCalls, 1)
			return nil
		},
	}

	req := sip.NewRequest(sip.INVITE, sip.Uri{Host: "example.invalid"})
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{User: "bes_123", Host: "example.invalid"},
		Params:  sip.NewParams(),
	})
	req.SetSource("127.0.0.1:5060")

	ds := &sipgo.DialogServerSession{}
	ds.InviteRequest = req
	ds.Init()

	dialog := &sipgox.DialogServerSession{
		DialogServerSession: ds,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		done <- b.handleAnsweredDialog(ctx, dialog)
	}()

	select {
	case <-done:
		t.Fatalf("handleAnsweredDialog returned too early; want it to block until dialog.Context().Done()")
	case <-time.After(50 * time.Millisecond):
	}

	// Завершаем dialog.Context() напрямую: в библиотеке cancel не экспортирован.
	// Для теста используем reflect/unsafe, чтобы вызвать context.CancelCauseFunc.
	df := reflect.ValueOf(ds).Elem().FieldByName("Dialog").FieldByName("cancel")
	if !df.IsValid() {
		t.Fatalf("cannot access Dialog.cancel via reflection")
	}
	cancelPtr := unsafe.Pointer(df.UnsafeAddr())
	dialogCancel := *(*context.CancelCauseFunc)(cancelPtr)
	dialogCancel(nil)

	select {
	case exitAnswerLoop := <-done:
		if exitAnswerLoop {
			t.Fatalf("exitAnswerLoop: got true want false (dialog should end the wait, not ctx)")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatalf("handleAnsweredDialog did not return after dialog.Context done")
	}

	if atomic.LoadInt32(&sendCalls) != 0 {
		t.Fatalf("sendTo calls: got %d want %d", sendCalls, 0)
	}
}

func TestInitSIP_CleanupCalledOnSendToError(t *testing.T) {
	cfg := &config.Bucis{EC: config.EC{ListenPort8890: 8890}}
	sipIDs := newSipIDState(0)
	sipIDs.SetIP("123", "127.0.0.1")

	var cleanupCalls int32
	prevStart := startRTPSession
	prevStartFromConn := startRTPSessionFromConn
	startRTPSession = func(ctx context.Context, logger rtpsession.Logger, localPort int, remoteAddr *net.UDPAddr) (func(), <-chan struct{}, error) {
		return func() { atomic.AddInt32(&cleanupCalls, 1) }, make(chan struct{}), nil
	}
	startRTPSessionFromConn = func(ctx context.Context, logger rtpsession.Logger, conn *net.UDPConn, remoteAddr *net.UDPAddr) (func(), <-chan struct{}, error) {
		return func() { atomic.AddInt32(&cleanupCalls, 1) }, make(chan struct{}), nil
	}
	defer func() {
		startRTPSession = prevStart
		startRTPSessionFromConn = prevStartFromConn
	}()

	var sendCalls int32
	b := &BUCIS{
		logger:    nopLogger{},
		cfg:       cfg,
		sipIDs:    sipIDs,
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo: func(string, int, string) error {
			atomic.AddInt32(&sendCalls, 1)
			return errors.New("send failed")
		},
	}

	req := sip.NewRequest(sip.INVITE, sip.Uri{Host: "example.invalid"})
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{User: "bes_123", Host: "example.invalid"},
		Params:  sip.NewParams(),
	})
	req.SetSource("127.0.0.1:5060")

	ds := &sipgo.DialogServerSession{}
	ds.InviteRequest = req
	ds.Init()

	dialog := &sipgox.DialogServerSession{
		DialogServerSession: ds,
		MediaSession: &media.MediaSession{
			Laddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000},
			Raddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40002},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = b.handleAnsweredDialog(ctx, dialog)

	if atomic.LoadInt32(&sendCalls) != 1 {
		t.Fatalf("sendTo calls: got %d want %d", sendCalls, 1)
	}
	if atomic.LoadInt32(&cleanupCalls) != 1 {
		t.Fatalf("cleanup calls: got %d want %d", cleanupCalls, 1)
	}
}

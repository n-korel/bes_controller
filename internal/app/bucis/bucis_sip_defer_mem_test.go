package bucis

import (
	"context"
	"errors"
	"net"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtpsession"

	"github.com/emiago/media"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/emiago/sipgox"
)

// Запустить 10 мок-диалогов подряд; HeapAlloc после GC не должен расти линейно.
func TestInitSIP_DeferDoesNotAccumulate(t *testing.T) {
	const (
		iters     = 10
		allocSize = 2 << 20 // 2 MiB на диалог; достаточно, чтобы проявить удержание.
	)

	prevStart := startRTPSession
	prevStartFromConn := startRTPSessionFromConn
	startRTPSession = func(ctx context.Context, logger rtpsession.Logger, localPort int, remoteAddr *net.UDPAddr) (func(), <-chan struct{}, error) {
		done := make(chan struct{})
		close(done)

		// Если cleanup/ctxCancel не выполняются, этот срез будет удерживаться и HeapAlloc начнёт расти ~allocSize/итерацию.
		buf := make([]byte, allocSize)
		return func() { _ = buf[0] }, done, nil
	}
	startRTPSessionFromConn = func(ctx context.Context, logger rtpsession.Logger, conn *net.UDPConn, remoteAddr *net.UDPAddr) (func(), <-chan struct{}, error) {
		done := make(chan struct{})
		close(done)

		buf := make([]byte, allocSize)
		return func() { _ = buf[0] }, done, nil
	}
	defer func() {
		startRTPSession = prevStart
		startRTPSessionFromConn = prevStartFromConn
	}()

	cfg := &config.Bucis{EC: config.EC{ListenPort8890: 8890}}
	sipIDs := newSipIDState(0)
	sipIDs.SetIP("123", "127.0.0.1")

	b := &BUCIS{
		logger:    nopLogger{},
		cfg:       cfg,
		sipIDs:    sipIDs,
		localIP:   "127.0.0.1",
		sipDomain: "127.0.0.1",
		sendTo:    func(string, int, string) error { return errors.New("send failed") },
	}

	readHeap := func() uint64 {
		runtime.GC()
		debug.FreeOSMemory()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}

	base := readHeap()
	max := base
	min := base

	for i := 0; i < iters; i++ {
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
				Laddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000 + i*2},
				Raddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40002 + i*2},
			},
		}

		iterCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = b.handleAnsweredDialog(iterCtx, dialog)
		cancel()

		h := readHeap()
		if h > max {
			max = h
		}
		if h < min {
			min = h
		}
	}

	// Если где-то накапливаются deferred cleanup'ы/контексты, рост будет около iters*allocSize.
	// Здесь ожидаем, что после GC память вернётся близко к baseline, допускаем небольшой шум аллокатора.
	if max-min > 4<<20 {
		t.Fatalf("HeapAlloc looks like it grows across dialogs: base=%d min=%d max=%d delta=%d", base, min, max, max-min)
	}
}

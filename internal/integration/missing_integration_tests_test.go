package integration

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	besapp "bucis-bes_simulator/internal/app/bes"
	bucisapp "bucis-bes_simulator/internal/app/bucis"
	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtpsession"
)

func listenUDP4(t *testing.T, ip string, port int) *net.UDPConn {
	t.Helper()
	lip := net.ParseIP(ip)
	if lip == nil {
		t.Fatalf("invalid ip %q", ip)
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lip, Port: port})
	if err != nil {
		t.Fatalf("ListenUDP(%s:%d): %v", ip, port, err)
	}
	return c
}

func udpSend(t *testing.T, laddr *net.UDPAddr, raddr *net.UDPAddr, payload string) {
	t.Helper()
	c, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := c.Write([]byte(payload)); err != nil {
		t.Fatalf("udp write: %v", err)
	}
}

func sendUntil(ctx context.Context, interval time.Duration, send func(), ok func() bool) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if ok() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			send()
		}
	}
}

func TestIntegration_ResetDuringQuery(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	p8890 := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_BES_QUERY_DST_PORT", strconv.Itoa(q6710))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(p8890))
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_ADDR", "")

	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")
	t.Setenv("EC_CLIENT_ANSWER_TIMEOUT", "2s")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "1")

	// SIP часть bucis выключаем: вместо OpenSIPS будет SIP-mock внутри bes.
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	mac := "AA:BB:CC:DD:EE:FF"
	t.Setenv("EC_MAC", mac)

	besCfg, err := config.ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resetCh := make(chan struct{}, 8)
	pressCh := make(chan struct{}, 1)
	gotAnswerCh := make(chan *protocol.ClientAnswer, 8)
	sipRan := make(chan struct{}, 1)

	var queries atomic.Int32
	querySeenCh := make(chan *net.UDPAddr, 8)
	allowAnswerCh := make(chan struct{}, 1)

	// Fake "BUCIS": слушает ClientQuery и по сигналу отвечает ClientAnswer.
	bucisDone := make(chan error, 1)
	go func() {
		conn := listenUDP4(t, "127.0.0.1", q6710)
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 2048)
		for ctx.Err() == nil {
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, raddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				if ctx.Err() != nil {
					break
				}
				bucisDone <- err
				return
			}
			pkt, ok := protocol.Parse(buf[:n])
			if !ok || pkt.Type != protocol.ECPacketClientQuery || pkt.Query == nil {
				continue
			}
			nq := queries.Add(1)
			select {
			case querySeenCh <- raddr:
			default:
			}
			// В этом тесте мы намеренно "теряем" ответ на первый query (чтобы reset отменил doPress),
			// и отвечаем только на второй.
			if nq == 2 {
				select {
				case <-ctx.Done():
					bucisDone <- ctx.Err()
					return
				case <-allowAnswerCh:
				}
				udpSend(t, nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}, protocol.FormatClientAnswer("127.0.0.1", "42"))
			}
		}
		bucisDone <- nil
	}()

	// BES.
	besDone := make(chan error, 1)
	go func() {
		besDone <- besapp.Run(ctx, nopLogger{}, besCfg, besapp.Options{
			MAC:                      mac,
			PressCh:                  pressCh,
			AutoPressAfterFirstReset: false,
			OnResetReceived:          resetCh,
			OnAnswerReceived:         gotAnswerCh,
			RunSIP: func(
				ctx context.Context,
				logger besapp.Logger,
				cfg config.Bes,
				domain string,
				sipID string,
				conversations <-chan string,
				dialEstablished chan<- struct{},
			) error {
				select {
				case sipRan <- struct{}{}:
				default:
				}
				return nil
			},
		})
	}()

	// Даем BES зарегистрироваться.
	resetAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}
	gotReset := atomic.Bool{}
	go func() {
		select {
		case <-resetCh:
			gotReset.Store(true)
		case <-ctx.Done():
		}
	}()
	sendUntil(ctx, 50*time.Millisecond, func() {
		udpSend(t, nil, resetAddr, protocol.FormatClientReset("127.0.0.1", "127.0.0.1"))
	}, gotReset.Load)
	if !gotReset.Load() {
		t.Fatalf("timeout waiting ClientReset")
	}

	// 1) press => query #1, но ответа нет.
	select {
	case pressCh <- struct{}{}:
	default:
	}
	select {
	case <-querySeenCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientQuery #1")
	}

	// Reset во время doPress (когда BES ждёт ClientAnswer).
	udpSend(t, nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}, protocol.FormatClientReset("127.0.0.1", "127.0.0.1"))

	// 2) press после reset => query #2.
	// Важно: reset отменяет doPress, а FSM дренирует pressCh; поэтому второй press шлём "до тех пор",
	// пока реально не увидим второй ClientQuery.
	press2Stop := make(chan struct{})
	defer close(press2Stop)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if queries.Load() >= 2 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-press2Stop:
				return
			case <-ticker.C:
				select {
				case pressCh <- struct{}{}:
				default:
				}
			}
		}
	}()
	select {
	case <-querySeenCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientQuery #2")
	}
	select {
	case allowAnswerCh <- struct{}{}:
	default:
	}

	select {
	case <-sipRan:
	case <-ctx.Done():
		t.Fatalf("timeout waiting RunSIP call (BES stuck after reset during query?)")
	}

	if got := queries.Load(); got < 2 {
		t.Fatalf("expected at least 2 ClientQuery packets, got %d", got)
	}

	cancel()
	<-besDone
	<-bucisDone
}

func TestIntegration_DuplicateClientQuery_SameMac(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	p8890 := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_BES_QUERY_DST_PORT", strconv.Itoa(q6710))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(p8890))
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_ADDR", "")

	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")
	t.Setenv("EC_CLIENT_ANSWER_TIMEOUT", "2s")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "1")

	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	mac := "AA:BB:CC:DD:EE:FF"
	t.Setenv("EC_MAC", mac)

	besCfg, err := config.ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resetCh := make(chan struct{}, 8)
	pressCh := make(chan struct{}, 1)

	secondSendDone := make(chan struct{}, 1)

	// BES: без настоящего SIP.
	besDone := make(chan error, 1)
	go func() {
		besDone <- besapp.Run(ctx, nopLogger{}, besCfg, besapp.Options{
			MAC:                      mac,
			PressCh:                  pressCh,
			AutoPressAfterFirstReset: false,
			OnResetReceived:          resetCh,
			RunSIP: func(context.Context, besapp.Logger, config.Bes, string, string, <-chan string, chan<- struct{}) error {
				return nil
			},
		})
	}()

	// Пока BES ещё в stateUnregistered и НЕ читает pressCh:
	pressCh <- struct{}{}
	go func() {
		pressCh <- struct{}{}
		select {
		case secondSendDone <- struct{}{}:
		default:
		}
	}()

	select {
	case <-secondSendDone:
		t.Fatalf("second press send must be blocked by pressCh buffer=1 (BES not reading yet)")
	case <-time.After(50 * time.Millisecond):
	}

	// Теперь шлём reset, BES начинает читать pressCh и второй send должен пройти.
	udpSend(t, nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}, protocol.FormatClientReset("127.0.0.1", "127.0.0.1"))
	select {
	case <-resetCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientReset")
	}
	select {
	case <-secondSendDone:
	case <-ctx.Done():
		t.Fatalf("timeout waiting second press send to unblock")
	}

	cancel()
	<-besDone
}

func TestIntegration_KeepaliveHeartbeat_TimingTest(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	p8890 := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(p8890))
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_ADDR", "")

	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "100ms")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Ловим keepalive пакеты "как BES".
	lconn := listenUDP4(t, "127.0.0.1", p8890)
	defer func() { _ = lconn.Close() }()

	keepaliveInterval := 100 * time.Millisecond
	bucisDone := make(chan error, 1)
	go func() {
		bucisDone <- bucisapp.Run(ctx, nopLogger{}, bucisapp.Options{
			BesBroadcastAddr:    "127.0.0.1",
			ClientResetInterval: time.Hour,
			KeepAliveInterval:   keepaliveInterval,
		})
	}()

	var times []time.Time
	buf := make([]byte, 2048)
	for len(times) < 6 && ctx.Err() == nil {
		_ = lconn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := lconn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			t.Fatalf("udp read: %v", err)
		}
		pkt, ok := protocol.Parse(buf[:n])
		if !ok || pkt.Type != protocol.ECPacketServerKeepAlive {
			continue
		}
		times = append(times, time.Now())
	}
	if len(times) < 4 {
		t.Fatalf("expected at least 4 keepalive packets, got %d", len(times))
	}

	min := keepaliveInterval * 5 / 10
	max := keepaliveInterval * 15 / 10
	for i := 2; i < len(times); i++ { // пропускаем самый первый (стартовый) + первый тик, проверяем устойчиво по хвосту
		d := times[i].Sub(times[i-1])
		if d < min || d > max {
			t.Fatalf("keepalive interval out of bounds: got %s want %s..%s", d.String(), min.String(), max.String())
		}
	}

	cancel()
	if err := <-bucisDone; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bucis returned error: %v", err)
	}
}

func TestIntegration_PortRange_AllPortsBusy(t *testing.T) {
	// Этот тест проверяет поведение при занятом RTP порте (эквивалентно "все порты заняты" для диапазона 1 порт).
	p := freeUDPPort(t)
	busy := listenUDP4(t, "127.0.0.1", p)
	defer func() { _ = busy.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	remote := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	_, _, err := rtpsession.Start(ctx, nopLogger{}, p, remote)
	if err == nil {
		t.Fatalf("expected rtpsession.Start to fail when local port is busy")
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("expected EADDRINUSE, got %v", err)
	}
}

func TestBUCIS_Answer_Unicast_To_Sender_Not_Broadcast(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	p8890 := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(p8890))
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "255.255.255.255") // должен НЕ влиять на ClientAnswer
	t.Setenv("EC_BES_ADDR", "")

	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	answerCh := make(chan bucisapp.AnswerEvent, 8)
	bucisDone := make(chan error, 1)
	go func() {
		bucisDone <- bucisapp.Run(ctx, nopLogger{}, bucisapp.Options{
			BesBroadcastAddr:    "255.255.255.255",
			ClientResetInterval: time.Hour,
			KeepAliveInterval:   time.Hour,
			OnAnswerSent:        answerCh,
			OpenSIPSIP:          "127.0.0.1",
		})
	}()

	// Отправляем ClientQuery с "нестандартного" loopback IP, чтобы проверить unicast на senderIP.
	senderIP := net.IPv4(127, 0, 0, 99)
	qconn := listenUDP4(t, senderIP.String(), 0)
	defer func() { _ = qconn.Close() }()
	_, qportStr, _ := net.SplitHostPort(qconn.LocalAddr().String())
	_ = qportStr

	raddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}

	gotAny := atomic.Bool{}
	var got bucisapp.AnswerEvent
	go func() {
		select {
		case got = <-answerCh:
			gotAny.Store(true)
		case <-ctx.Done():
		}
	}()
	sendUntil(ctx, 50*time.Millisecond, func() {
		_ = qconn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		qLine, qerr := protocol.FormatClientQuery("AA:BB:CC:DD:EE:FF")
		if qerr != nil {
			t.Fatalf("format query: %v", qerr)
		}
		_, _ = qconn.WriteToUDP([]byte(qLine), raddr)
	}, gotAny.Load)
	if !gotAny.Load() {
		t.Fatalf("timeout waiting AnswerEvent")
	}
	if got.DstPort != p8890 {
		t.Fatalf("DstPort: got %d want %d", got.DstPort, p8890)
	}
	if got.DstIP != senderIP.String() {
		t.Fatalf("DstIP: got %q want sender ip %q (ClientAnswer must be unicast to src_ip)", got.DstIP, senderIP.String())
	}

	cancel()
	<-bucisDone
}

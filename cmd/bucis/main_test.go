package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	app "bucis-bes_simulator/internal/app/bucis"
	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/pkg/log"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	la, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = c.Close()
		t.Fatalf("LocalAddr: want *net.UDPAddr, got %T", c.LocalAddr())
	}
	p := la.Port
	_ = c.Close()
	return p
}

func TestBUCIS_QueryAnswer_StableSipID(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")
	t.Setenv("EC_SIPID_MODULO", "")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	// Чтобы тест не пытался слать в broadcast и не спамил по таймерам.
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть должна быть отключена.
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, logger, app.Options{})
	}()

	answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		cancel()
		t.Fatalf("listen answer: %v", err)
	}
	defer func() { _ = answerConn.Close() }()

	queryConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		cancel()
		t.Fatalf("listen query: %v", err)
	}
	defer func() { _ = queryConn.Close() }()
	queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}

	readAnswerUntil := func(deadline time.Time) (*protocol.ClientAnswer, error) {
		t.Helper()
		buf := make([]byte, 2048)
		for {
			_ = answerConn.SetReadDeadline(deadline)
			n, _, err := answerConn.ReadFromUDP(buf)
			if err != nil {
				return nil, fmt.Errorf("read answer: %w", err)
			}
			pkt, ok := protocol.Parse(buf[:n])
			if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
				return pkt.Answer, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	writeQuery := func(mac string) {
		t.Helper()
		payload, ferr := protocol.FormatClientQuery(mac)
		if ferr != nil {
			t.Fatalf("format query: %v", ferr)
		}
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := queryConn.WriteToUDP([]byte(payload), queryDst); err != nil {
			t.Fatalf("write query: %v", err)
		}
	}

	queryAndGet := func(mac string) *protocol.ClientAnswer {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for attempt := 0; attempt < 25; attempt++ {
			writeQuery(mac)
			ans, err := readAnswerUntil(time.Now().Add(150 * time.Millisecond))
			if err == nil {
				return ans
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for ec_client_answer for mac=%q (last err=%v)", mac, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for ec_client_answer for mac=%q", mac)
		return nil
	}

	ans1 := queryAndGet("AA:BB:CC:DD:EE:01")
	if ans1.NewIP != "127.0.0.1" {
		t.Fatalf("NewIP: got %q want %q", ans1.NewIP, "127.0.0.1")
	}
	if ans1.SipID == "" {
		t.Fatalf("SipID must not be empty")
	}

	ans2 := queryAndGet("AA:BB:CC:DD:EE:01")
	if ans2.SipID != ans1.SipID {
		t.Fatalf("SipID must be stable for same MAC: got %q then %q", ans1.SipID, ans2.SipID)
	}

	ans3 := queryAndGet("AA:BB:CC:DD:EE:02")
	if ans3.SipID == ans1.SipID {
		t.Fatalf("SipID must differ for different MAC: got %q and %q", ans1.SipID, ans3.SipID)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run did not stop after context cancel")
	}
}

func TestBUCIS_SipID_StableAfterModulo_ZeroSipID_NeverReturned(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")
	t.Setenv("EC_SIPID_MODULO", "2")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	// Чтобы тест не пытался слать в broadcast и не спамил по таймерам.
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть должна быть отключена.
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, logger, app.Options{})
	}()

	answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		cancel()
		t.Fatalf("listen answer: %v", err)
	}
	defer func() { _ = answerConn.Close() }()

	queryConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		cancel()
		t.Fatalf("listen query: %v", err)
	}
	defer func() { _ = queryConn.Close() }()
	queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}

	readAnswerUntil := func(deadline time.Time) (*protocol.ClientAnswer, error) {
		t.Helper()
		buf := make([]byte, 2048)
		for {
			_ = answerConn.SetReadDeadline(deadline)
			n, _, err := answerConn.ReadFromUDP(buf)
			if err != nil {
				return nil, fmt.Errorf("read answer: %w", err)
			}
			pkt, ok := protocol.Parse(buf[:n])
			if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
				return pkt.Answer, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	writeQuery := func(mac string) {
		t.Helper()
		payload, ferr := protocol.FormatClientQuery(mac)
		if ferr != nil {
			t.Fatalf("format query: %v", ferr)
		}
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := queryConn.WriteToUDP([]byte(payload), queryDst); err != nil {
			t.Fatalf("write query: %v", err)
		}
	}

	queryAndGet := func(mac string) *protocol.ClientAnswer {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for attempt := 0; attempt < 25; attempt++ {
			writeQuery(mac)
			ans, err := readAnswerUntil(time.Now().Add(150 * time.Millisecond))
			if err == nil {
				return ans
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for ec_client_answer for mac=%q (last err=%v)", mac, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for ec_client_answer for mac=%q", mac)
		return nil
	}

	// modulo=2 даёт sipID "1" для первого нового MAC (seq=1) и "2" для второго (seq=2),
	// при этом "0" никогда не должен возвращаться.
	ans1 := queryAndGet("AA:BB:CC:DD:EE:41")
	if ans1.SipID == "" || ans1.SipID == "0" {
		t.Fatalf("SipID must not be empty or zero: %q", ans1.SipID)
	}

	ans1b := queryAndGet("AA:BB:CC:DD:EE:41")
	if ans1b.SipID != ans1.SipID {
		t.Fatalf("SipID must be stable after modulo for same MAC: got %q then %q", ans1.SipID, ans1b.SipID)
	}

	ans2 := queryAndGet("AA:BB:CC:DD:EE:42")
	if ans2.SipID == "" || ans2.SipID == "0" {
		t.Fatalf("SipID must not be empty or zero: %q", ans2.SipID)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run did not stop after context cancel")
	}
}

func TestBUCIS_QueryAnswer_SipID_NotStableAcrossRestarts_NoPersistence(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	// Без персистентности "между рестартами" sipID для одного MAC не обязан быть стабильным по спеке.
	// Этот тест фиксирует ожидаемое поведение: после перезапуска bucis выдаётся новый sipID.
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть должна быть отключена.
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		t.Fatalf("listen answer: %v", err)
	}
	defer func() { _ = answerConn.Close() }()

	queryConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen query: %v", err)
	}
	defer func() { _ = queryConn.Close() }()
	queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}

	readAnswerUntil := func(deadline time.Time) (*protocol.ClientAnswer, error) {
		t.Helper()
		buf := make([]byte, 2048)
		for {
			_ = answerConn.SetReadDeadline(deadline)
			n, _, err := answerConn.ReadFromUDP(buf)
			if err != nil {
				return nil, fmt.Errorf("read answer: %w", err)
			}
			pkt, ok := protocol.Parse(buf[:n])
			if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
				return pkt.Answer, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	writeQuery := func(mac string) {
		t.Helper()
		payload, ferr := protocol.FormatClientQuery(mac)
		if ferr != nil {
			t.Fatalf("format query: %v", ferr)
		}
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := queryConn.WriteToUDP([]byte(payload), queryDst); err != nil {
			t.Fatalf("write query: %v", err)
		}
	}

	queryAndGet := func(mac string) *protocol.ClientAnswer {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for attempt := 0; attempt < 25; attempt++ {
			writeQuery(mac)
			ans, err := readAnswerUntil(time.Now().Add(150 * time.Millisecond))
			if err == nil {
				return ans
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for ec_client_answer for mac=%q (last err=%v)", mac, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for ec_client_answer for mac=%q", mac)
		return nil
	}

	runOnce := func() (stop func(), done <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan error, 1)
		go func() { ch <- app.Run(ctx, logger, app.Options{}) }()
		return cancel, ch
	}

	mac := "AA:BB:CC:DD:EE:31"

	stop1, done1 := runOnce()
	ans1 := queryAndGet(mac)
	if ans1.SipID == "" {
		stop1()
		t.Fatalf("SipID must not be empty")
	}
	stop1()
	select {
	case err := <-done1:
		if err != nil {
			t.Fatalf("run(1) returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run(1) did not stop after context cancel")
	}

	// Пробуем вычитать всё, что могло "догнаться" в сокет после остановки первого запуска.
	{
		buf := make([]byte, 2048)
		deadline := time.Now().Add(50 * time.Millisecond)
		for {
			_ = answerConn.SetReadDeadline(deadline)
			if _, _, err := answerConn.ReadFromUDP(buf); err != nil {
				break
			}
			if time.Now().After(deadline) {
				break
			}
		}
	}

	stop2, done2 := runOnce()
	ans2 := queryAndGet(mac)
	if ans2.SipID == "" {
		stop2()
		t.Fatalf("SipID must not be empty")
	}
	stop2()
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("run(2) returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run(2) did not stop after context cancel")
	}

	if ans2.SipID == ans1.SipID {
		t.Fatalf("SipID must change across restarts without persistence for same MAC: got %q then %q", ans1.SipID, ans2.SipID)
	}
}

func TestBUCIS_Reset_HeadSelection_FlagsOverEnv_AndHead2DefaultsToHead1(t *testing.T) {
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	// query порты не используются в этом тесте, но run их откроет
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(freeUDPPort(t)))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(freeUDPPort(t)))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	// чтобы не спамить в broadcast и по таймерам
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть должна быть отключена
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	// env head1/head2, но флаг head1 должен иметь приоритет, head2 пустой -> = head1
	t.Setenv("EC_BUCIS_HEAD1_IP", "10.10.10.10")
	t.Setenv("EC_BUCIS_HEAD2_IP", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, logger, app.Options{Head1IP: "1.1.1.1"})
	}()

	lconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		cancel()
		t.Fatalf("listen 8890: %v", err)
	}
	defer func() { _ = lconn.Close() }()

	buf := make([]byte, 2048)
	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = lconn.SetReadDeadline(deadline)
		n, _, err := lconn.ReadFromUDP(buf)
		if err != nil {
			cancel()
			t.Fatalf("read 8890: %v", err)
		}
		pkt, ok := protocol.Parse(buf[:n])
		if ok && pkt.Type == protocol.ECPacketClientReset && pkt.Reset != nil {
			if pkt.Reset.IPHead1 != "1.1.1.1" {
				t.Fatalf("head1: got %q want %q", pkt.Reset.IPHead1, "1.1.1.1")
			}
			if pkt.Reset.IPHead2 != "1.1.1.1" {
				t.Fatalf("head2: got %q want %q", pkt.Reset.IPHead2, "1.1.1.1")
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("timeout waiting for ec_client_reset")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run did not stop after context cancel")
	}
}

func TestBUCIS_Reset_SentOnlyOnce_EvenIfIntervalIsSmall(t *testing.T) {
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	// query порты не используются в этом тесте, но run их откроет
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(freeUDPPort(t)))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(freeUDPPort(t)))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	// reset-interval ставим маленьким; keepalive отключаем большим, чтобы не шумел в тесте
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "10ms")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть должна быть отключена
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, logger, app.Options{})
	}()

	lconn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		cancel()
		t.Fatalf("listen 8890: %v", err)
	}
	defer func() { _ = lconn.Close() }()

	buf := make([]byte, 2048)

	// 1) Дождаться первого reset.
	var gotFirst bool
	waitFirstDeadline := time.Now().Add(2 * time.Second)
	for {
		_ = lconn.SetReadDeadline(waitFirstDeadline)
		n, _, err := lconn.ReadFromUDP(buf)
		if err != nil {
			cancel()
			t.Fatalf("read 8890: %v", err)
		}
		pkt, ok := protocol.Parse(buf[:n])
		if ok && pkt.Type == protocol.ECPacketClientReset && pkt.Reset != nil {
			gotFirst = true
			break
		}
		if time.Now().After(waitFirstDeadline) {
			cancel()
			t.Fatalf("timeout waiting for first ec_client_reset")
		}
	}
	if !gotFirst {
		cancel()
		t.Fatalf("did not receive first ec_client_reset")
	}

	// 2) Проверить, что второго reset не приходит.
	// Интервал в 10ms означает, что при старой логике повтор пришёл бы многократно.
	noSecondUntil := time.Now().Add(150 * time.Millisecond)
	for {
		_ = lconn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		n, _, err := lconn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if time.Now().After(noSecondUntil) {
					break
				}
				continue
			}
			cancel()
			t.Fatalf("read 8890: %v", err)
		}
		pkt, ok := protocol.Parse(buf[:n])
		if ok && pkt.Type == protocol.ECPacketClientReset && pkt.Reset != nil {
			cancel()
			t.Fatalf("unexpected repeated ec_client_reset")
		}
		if time.Now().After(noSecondUntil) {
			break
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("run did not stop after context cancel")
	}
}

func TestBUCIS_Answer_NewIP_PriorityFlagsOverEnv(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))

	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP часть отключена
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	// env домен есть, но флаг opensipsIP должен быть приоритетнее для NewIP в ответе
	t.Setenv("SIP_DOMAIN", "9.9.9.9")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, logger, app.Options{OpenSIPSIP: "1.2.3.4"})
	}()

	answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
	if err != nil {
		cancel()
		t.Fatalf("listen answer: %v", err)
	}
	defer func() { _ = answerConn.Close() }()

	queryConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		cancel()
		t.Fatalf("listen query: %v", err)
	}
	defer func() { _ = queryConn.Close() }()
	queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}

	readAnswer := func(deadline time.Time) (*protocol.ClientAnswer, error) {
		buf := make([]byte, 2048)
		for {
			_ = answerConn.SetReadDeadline(deadline)
			n, _, err := answerConn.ReadFromUDP(buf)
			if err != nil {
				return nil, fmt.Errorf("read answer: %w", err)
			}
			pkt, ok := protocol.Parse(buf[:n])
			if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
				return pkt.Answer, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		payload, ferr := protocol.FormatClientQuery("AA:BB:CC:DD:EE:11")
		if ferr != nil {
			t.Fatalf("format query: %v", ferr)
		}
		_, err := queryConn.WriteToUDP([]byte(payload), queryDst)
		if err != nil {
			t.Fatalf("write query: %v", err)
		}
		ans, err := readAnswer(time.Now().Add(150 * time.Millisecond))
		if err == nil {
			if ans.NewIP != "1.2.3.4" {
				t.Fatalf("NewIP: got %q want %q", ans.NewIP, "1.2.3.4")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for ec_client_answer (last err=%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestBUCIS_BesAddrOverride_SendsAnswerToOverrideNotSenderIP(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(answerPort))
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	// SIP отключён
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis-test")

	t.Run("no_override_answer_arrives_on_sender_ip", func(t *testing.T) {
		// без override: ответ должен прийти на 127.0.0.1:8890
		t.Setenv("EC_BES_ADDR", "")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- app.Run(ctx, logger, app.Options{})
		}()

		answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
		if err != nil {
			t.Fatalf("listen answer: %v", err)
		}
		defer func() { _ = answerConn.Close() }()

		queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}
		queryConn, err := net.DialUDP("udp4", nil, queryDst)
		if err != nil {
			t.Fatalf("dial query: %v", err)
		}
		defer func() { _ = queryConn.Close() }()

		buf := make([]byte, 2048)
		deadline := time.Now().Add(2 * time.Second)
		for {
			_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			payload, ferr := protocol.FormatClientQuery("AA:BB:CC:DD:EE:21")
			if ferr != nil {
				t.Fatalf("format query: %v", ferr)
			}
			_, _ = queryConn.Write([]byte(payload))

			_ = answerConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			n, _, err := answerConn.ReadFromUDP(buf)
			if err == nil {
				pkt, ok := protocol.Parse(buf[:n])
				if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("expected ec_client_answer on 127.0.0.1 when override is empty (last err=%v)", err)
			}
		}

		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})

	t.Run("override_answer_does_not_arrive_on_sender_ip", func(t *testing.T) {
		// с override на заведомо "чужой" IP: listener 127.0.0.1:8890 не должен получать answer
		t.Setenv("EC_BES_ADDR", "192.0.2.1") // TEST-NET-1

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- app.Run(ctx, logger, app.Options{})
		}()

		answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
		if err != nil {
			t.Fatalf("listen answer: %v", err)
		}
		defer func() { _ = answerConn.Close() }()

		queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}
		queryConn, err := net.DialUDP("udp4", nil, queryDst)
		if err != nil {
			t.Fatalf("dial query: %v", err)
		}
		defer func() { _ = queryConn.Close() }()

		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		payload, ferr := protocol.FormatClientQuery("AA:BB:CC:DD:EE:22")
		if ferr != nil {
			t.Fatalf("format query: %v", ferr)
		}
		if _, err := queryConn.Write([]byte(payload)); err != nil {
			t.Fatalf("write query: %v", err)
		}

		buf := make([]byte, 2048)
		deadline := time.Now().Add(350 * time.Millisecond)
		for {
			_ = answerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := answerConn.ReadFromUDP(buf)
			if err == nil {
				pkt, ok := protocol.Parse(buf[:n])
				if ok && pkt.Type == protocol.ECPacketClientAnswer && pkt.Answer != nil {
					t.Fatalf("did not expect ec_client_answer on 127.0.0.1 when override points to 192.0.2.1")
				}
			}
			if time.Now().After(deadline) {
				break
			}
		}

		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})
}

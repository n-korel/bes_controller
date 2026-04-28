package main

import (
	"context"
	"flag"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/pkg/log"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	p := c.LocalAddr().(*net.UDPAddr).Port
	_ = c.Close()
	return p
}

func TestBUCIS_QueryAnswer_StableSipID(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	answerPort := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

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
		fs := flag.NewFlagSet("bucis-test", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		done <- run(ctx, logger, fs, "", "", "", 0, 0, "", "")
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
				return nil, err
			}
			_, _, ans, _, _, ok := protocol.Parse(buf[:n])
			if ok && ans != nil {
				return ans, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	writeQuery := func(mac string) {
		t.Helper()
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := queryConn.WriteToUDP([]byte(protocol.FormatClientQuery(mac)), queryDst); err != nil {
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
		fs := flag.NewFlagSet("bucis-test", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		done <- run(ctx, logger, fs, "", "", "", 0, 0, "1.1.1.1", "")
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
		reset, _, _, _, _, ok := protocol.Parse(buf[:n])
		if ok && reset != nil {
			if reset.IPHead1 != "1.1.1.1" {
				t.Fatalf("head1: got %q want %q", reset.IPHead1, "1.1.1.1")
			}
			if reset.IPHead2 != "1.1.1.1" {
				t.Fatalf("head2: got %q want %q", reset.IPHead2, "1.1.1.1")
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
		fs := flag.NewFlagSet("bucis-test", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		done <- run(ctx, logger, fs, "", "", "1.2.3.4", 0, 0, "", "")
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
				return nil, err
			}
			_, _, ans, _, _, ok := protocol.Parse(buf[:n])
			if ok && ans != nil {
				return ans, nil
			}
			if time.Now().After(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err := queryConn.WriteToUDP([]byte(protocol.FormatClientQuery("AA:BB:CC:DD:EE:11")), queryDst)
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

	// 1) без override: ответ должен прийти на 127.0.0.1:8890
	{
		t.Setenv("EC_BES_ADDR", "")

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			fs := flag.NewFlagSet("bucis-test", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			done <- run(ctx, logger, fs, "", "", "", 0, 0, "", "")
		}()

		answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
		if err != nil {
			cancel()
			t.Fatalf("listen answer: %v", err)
		}

		queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}
		queryConn, err := net.DialUDP("udp4", nil, queryDst)
		if err != nil {
			cancel()
			_ = answerConn.Close()
			t.Fatalf("dial query: %v", err)
		}

		buf := make([]byte, 2048)
		deadline := time.Now().Add(2 * time.Second)
		for {
			_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			_, _ = queryConn.Write([]byte(protocol.FormatClientQuery("AA:BB:CC:DD:EE:21")))

			_ = answerConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			n, _, err := answerConn.ReadFromUDP(buf)
			if err == nil {
				_, _, ans, _, _, ok := protocol.Parse(buf[:n])
				if ok && ans != nil {
					break
				}
			}
			if time.Now().After(deadline) {
				cancel()
				_ = queryConn.Close()
				_ = answerConn.Close()
				t.Fatalf("expected ec_client_answer on 127.0.0.1 when override is empty (last err=%v)", err)
			}
		}

		cancel()
		_ = queryConn.Close()
		_ = answerConn.Close()
		<-done
	}

	// 2) с override на заведомо "чужой" IP: listener 127.0.0.1:8890 не должен получать answer
	{
		t.Setenv("EC_BES_ADDR", "192.0.2.1") // TEST-NET-1

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			fs := flag.NewFlagSet("bucis-test", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			done <- run(ctx, logger, fs, "", "", "", 0, 0, "", "")
		}()

		answerConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: answerPort})
		if err != nil {
			cancel()
			t.Fatalf("listen answer: %v", err)
		}

		queryDst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: q6710}
		queryConn, err := net.DialUDP("udp4", nil, queryDst)
		if err != nil {
			cancel()
			_ = answerConn.Close()
			t.Fatalf("dial query: %v", err)
		}

		_ = queryConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := queryConn.Write([]byte(protocol.FormatClientQuery("AA:BB:CC:DD:EE:22"))); err != nil {
			cancel()
			_ = queryConn.Close()
			_ = answerConn.Close()
			t.Fatalf("write query: %v", err)
		}

		buf := make([]byte, 2048)
		deadline := time.Now().Add(350 * time.Millisecond)
		for {
			_ = answerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := answerConn.ReadFromUDP(buf)
			if err == nil {
				_, _, ans, _, _, ok := protocol.Parse(buf[:n])
				if ok && ans != nil {
					cancel()
					_ = queryConn.Close()
					_ = answerConn.Close()
					t.Fatalf("did not expect ec_client_answer on 127.0.0.1 when override points to 192.0.2.1")
				}
			}
			if time.Now().After(deadline) {
				break
			}
		}

		cancel()
		_ = queryConn.Close()
		_ = answerConn.Close()
		<-done
	}
}

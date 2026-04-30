package integration

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	besapp "bucis-bes_simulator/internal/app/bes"
	bucisapp "bucis-bes_simulator/internal/app/bucis"
	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Debug(string, ...any) {}

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

func TestIntegration_FullECFlow(t *testing.T) {
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
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

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

	resetCh := make(chan struct{}, 1)
	pressCh := make(chan struct{}, 1)
	queryCh := make(chan bucisapp.QueryEvent, 8)
	answerCh := make(chan bucisapp.AnswerEvent, 8)
	convCh := make(chan string, 1)

	besDone := make(chan error, 1)
	go func() {
		besDone <- besapp.Run(ctx, nopLogger{}, besCfg, besapp.Options{
			MAC:                      mac,
			PressCh:                  pressCh,
			AutoPressAfterFirstReset: false,
			OnResetReceived:          resetCh,
			OnConversationReceived:   convCh,
			RunSIP: func(
				ctx context.Context,
				logger besapp.Logger,
				cfg config.Bes,
				domain string,
				sipID string,
				conversations <-chan string,
				dialEstablished chan<- struct{},
			) error {
				if sipID == "" {
					return fmt.Errorf("sipID is empty")
				}
				if dialEstablished != nil {
					select {
					case dialEstablished <- struct{}{}:
					default:
					}
				}

				raddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}
				conn, err := net.DialUDP("udp4", nil, raddr)
				if err != nil {
					return fmt.Errorf("dial udp %s: %w", raddr.String(), err)
				}
				_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
				_, _ = conn.Write([]byte(protocol.FormatClientConversation(sipID)))
				_ = conn.Close()

				deadline := time.NewTimer(2 * time.Second)
				defer deadline.Stop()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-deadline.C:
						return context.DeadlineExceeded
					case got := <-conversations:
						if got == sipID {
							return nil
						}
					}
				}
			},
		})
	}()

	bucisDone := make(chan error, 1)
	go func() {
		bucisDone <- bucisapp.Run(ctx, nopLogger{}, bucisapp.Options{
			BesBroadcastAddr:    "127.0.0.1",
			ClientResetInterval: time.Hour,
			KeepAliveInterval:   time.Hour,
			OnQueryReceived:     queryCh,
			OnAnswerSent:        answerCh,
		})
	}()

	select {
	case <-resetCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientReset")
	}

	select {
	case pressCh <- struct{}{}:
	default:
	}

	var gotQuery bucisapp.QueryEvent
	select {
	case gotQuery = <-queryCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientQuery")
	}
	if gotQuery.Port != q6710 {
		t.Fatalf("query port: got %d want %d", gotQuery.Port, q6710)
	}
	if gotQuery.MAC != mac {
		t.Fatalf("query mac: got %q want %q", gotQuery.MAC, mac)
	}

	var gotAnswer bucisapp.AnswerEvent
	select {
	case gotAnswer = <-answerCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientAnswer")
	}
	if gotAnswer.SipID == "" {
		t.Fatalf("SipID must not be empty")
	}
	if gotAnswer.NewIP != "127.0.0.1" {
		t.Fatalf("NewIP: got %q want %q", gotAnswer.NewIP, "127.0.0.1")
	}

	var gotConv string
	select {
	case gotConv = <-convCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientConversation")
	}
	if gotConv != gotAnswer.SipID {
		t.Fatalf("conversation sipID: got %q want %q", gotConv, gotAnswer.SipID)
	}

	cancel()

	select {
	case err := <-besDone:
		if err != nil {
			t.Fatalf("bes returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bes did not stop after cancel")
	}

	select {
	case err := <-bucisDone:
		if err != nil {
			t.Fatalf("bucis returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bucis did not stop after cancel")
	}
}

func TestIntegration_ClientQuery_Port7777(t *testing.T) {
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	p8890 := freeUDPPort(t)

	t.Setenv("LOG_FORMAT", "console")
	t.Setenv("SIP_DOMAIN", "127.0.0.1")

	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_BES_QUERY_DST_PORT", strconv.Itoa(q7777))
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(p8890))
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_ADDR", "")

	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

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

	resetCh := make(chan struct{}, 1)
	pressCh := make(chan struct{}, 1)
	queryCh := make(chan bucisapp.QueryEvent, 8)
	answerCh := make(chan bucisapp.AnswerEvent, 8)
	convCh := make(chan string, 1)

	besDone := make(chan error, 1)
	go func() {
		besDone <- besapp.Run(ctx, nopLogger{}, besCfg, besapp.Options{
			MAC:                      mac,
			PressCh:                  pressCh,
			AutoPressAfterFirstReset: false,
			OnResetReceived:          resetCh,
			OnConversationReceived:   convCh,
			RunSIP: func(
				ctx context.Context,
				logger besapp.Logger,
				cfg config.Bes,
				domain string,
				sipID string,
				conversations <-chan string,
				dialEstablished chan<- struct{},
			) error {
				if sipID == "" {
					return fmt.Errorf("sipID is empty")
				}
				if dialEstablished != nil {
					select {
					case dialEstablished <- struct{}{}:
					default:
					}
				}

				raddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p8890}
				conn, err := net.DialUDP("udp4", nil, raddr)
				if err != nil {
					return fmt.Errorf("dial udp %s: %w", raddr.String(), err)
				}
				_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
				_, _ = conn.Write([]byte(protocol.FormatClientConversation(sipID)))
				_ = conn.Close()

				deadline := time.NewTimer(2 * time.Second)
				defer deadline.Stop()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-deadline.C:
						return context.DeadlineExceeded
					case got := <-conversations:
						if got == sipID {
							return nil
						}
					}
				}
			},
		})
	}()

	bucisDone := make(chan error, 1)
	go func() {
		bucisDone <- bucisapp.Run(ctx, nopLogger{}, bucisapp.Options{
			BesBroadcastAddr:    "127.0.0.1",
			ClientResetInterval: time.Hour,
			KeepAliveInterval:   time.Hour,
			OnQueryReceived:     queryCh,
			OnAnswerSent:        answerCh,
		})
	}()

	select {
	case <-resetCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientReset")
	}

	select {
	case pressCh <- struct{}{}:
	default:
	}

	var gotQuery bucisapp.QueryEvent
	select {
	case gotQuery = <-queryCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientQuery")
	}
	if gotQuery.Port != q7777 {
		t.Fatalf("query port: got %d want %d", gotQuery.Port, q7777)
	}
	if gotQuery.MAC != mac {
		t.Fatalf("query mac: got %q want %q", gotQuery.MAC, mac)
	}

	var gotAnswer bucisapp.AnswerEvent
	select {
	case gotAnswer = <-answerCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientAnswer")
	}
	if gotAnswer.SipID == "" {
		t.Fatalf("SipID must not be empty")
	}
	if gotAnswer.NewIP != "127.0.0.1" {
		t.Fatalf("NewIP: got %q want %q", gotAnswer.NewIP, "127.0.0.1")
	}

	var gotConv string
	select {
	case gotConv = <-convCh:
	case <-ctx.Done():
		t.Fatalf("timeout waiting ClientConversation")
	}
	if gotConv != gotAnswer.SipID {
		t.Fatalf("conversation sipID: got %q want %q", gotConv, gotAnswer.SipID)
	}

	cancel()

	select {
	case err := <-besDone:
		if err != nil {
			t.Fatalf("bes returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bes did not stop after cancel")
	}

	select {
	case err := <-bucisDone:
		if err != nil {
			t.Fatalf("bucis returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bucis did not stop after cancel")
	}
}

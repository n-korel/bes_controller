package bes

import (
	"context"
	"net"
	"strings"
	"testing"

	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/internal/media/rtpsession"
)

func TestRunSIP_ValidationErrors(t *testing.T) {
	t.Run("invalid SIP_PORT", func(t *testing.T) {
		err := DefaultRunSIP(context.Background(), nopLogger{}, config.Bes{
			SIPUserBucis: "bucis",
			SIPUserBes:   "bes_1",
			SIPPort:      70000,
		}, "127.0.0.1", "1", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_PORT invalid") {
			t.Fatalf("expected SIP_PORT invalid error, got %v", err)
		}
	})

	t.Run("missing SIP_USER_BUCIS", func(t *testing.T) {
		err := DefaultRunSIP(context.Background(), nopLogger{}, config.Bes{
			SIPPort:    5060,
			SIPUserBes: "bes_1",
		}, "127.0.0.1", "1", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_USER_BUCIS must be set") {
			t.Fatalf("expected SIP_USER_BUCIS error, got %v", err)
		}
	})

	t.Run("missing SIP_USER_BES when sipID empty", func(t *testing.T) {
		err := DefaultRunSIP(context.Background(), nopLogger{}, config.Bes{
			SIPPort:      5060,
			SIPUserBucis: "bucis",
		}, "127.0.0.1", "", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_USER_BES must be set") {
			t.Fatalf("expected SIP_USER_BES error, got %v", err)
		}
	})
}

type captureLogger struct {
	infos []string
	warns []string
}

func (l *captureLogger) Info(msg string, _ ...any) { l.infos = append(l.infos, msg) }
func (l *captureLogger) Warn(msg string, _ ...any) { l.warns = append(l.warns, msg) }

// Симулирует захват порта — ожидаем ошибку без паники, с логированием.
func TestDefaultRunSIP_PortRaceCausesCleanError(t *testing.T) {
	t.Setenv("NO_AUDIO", "1")

	// Выбираем свободный UDP порт.
	l1, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("listen udp ephemeral: %v", err)
	}
	la, ok := l1.LocalAddr().(*net.UDPAddr)
	if !ok || la == nil {
		_ = l1.Close()
		t.Fatalf("unexpected local addr type: %T", l1.LocalAddr())
	}
	port := la.Port
	_ = l1.Close()

	// Имитируем "другой процесс", который занял порт между Close и Start.
	l2, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		t.Fatalf("listen udp occupy port %d: %v", port, err)
	}
	defer func() { _ = l2.Close() }()

	log := &captureLogger{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()

	_, _, startErr := rtpsession.Start(
		context.Background(),
		log,
		port,
		&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9},
	)
	if startErr == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(startErr.Error(), "rtp rx start") {
		t.Fatalf("expected rtp rx start error, got %v", startErr)
	}
	foundWarn := false
	for _, w := range log.warns {
		if strings.Contains(w, "rtp rx start failed") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected warn log 'rtp rx start failed', got warns=%v", log.warns)
	}
}

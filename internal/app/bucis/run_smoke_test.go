package bucis

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

type smokeLogger struct{}

func (smokeLogger) Info(string, ...any)  {}
func (smokeLogger) Warn(string, ...any)  {}
func (smokeLogger) Debug(string, ...any) {}

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

func TestRun_Smoke_StartStop_NoSIP(t *testing.T) {
	// Минимальные env для ParseBucis().
	q6710 := freeUDPPort(t)
	q7777 := freeUDPPort(t)
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_LISTEN_PORT_8890", strconv.Itoa(freeUDPPort(t)))
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", strconv.Itoa(q6710))
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", strconv.Itoa(q7777))
	t.Setenv("EC_BES_QUERY_DST_PORT", strconv.Itoa(q6710))

	// Чтобы Run не лез в SIP.
	t.Setenv("SIP_USER_BUCIS", "")
	t.Setenv("SIP_PASS_BUCIS", "")

	// Чтобы не плодить активность таймеров.
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "1h")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "1h")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, smokeLogger{}, Options{}) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not stop after context cancel")
	}
}

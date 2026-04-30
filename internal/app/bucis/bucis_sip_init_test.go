package bucis

import (
	"context"
	"sync"
	"testing"

	"bucis-bes_simulator/internal/infra/config"
)

type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Debug(string, ...any) {}

func TestBUCIS_InitSIP_EnabledButInvalidLocalIP_ReturnsFalse(t *testing.T) {
	t.Setenv("SIP_USER_BUCIS", "bucis")
	t.Setenv("SIP_PASS_BUCIS", "pass")
	t.Setenv("SIP_PORT", "5060")
	t.Setenv("SIP_REGISTER_TIMEOUT", "nope") // пройти ветку warning + default

	b := &BUCIS{
		logger:    nopLogger{},
		cfg:       &config.Bucis{EC: config.EC{ListenPort8890: 8890}},
		localIP:   "not-an-ip",
		sipDomain: "not-an-ip",
	}

	var wg sync.WaitGroup
	ok := b.initSIP(context.Background(), &wg)
	if ok {
		t.Fatalf("expected initSIP to return false")
	}
}

package bes

import (
	"context"
	"strings"
	"testing"

	"bucis-bes_simulator/internal/infra/config"
)

func TestRun_ListenUDPError(t *testing.T) {
	cfg := config.Bes{
		EC: config.EC{
			ListenPort8890:  -1,
			BucisAddr:       "127.0.0.1",
			BesQueryDstPort: 6710,
		},
		MAC: "AA:BB:CC:DD:EE:FF",
	}

	err := Run(context.Background(), nopLogger{}, cfg, Options{
		MAC: "AA:BB:CC:DD:EE:FF",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listen udp 8890") {
		t.Fatalf("unexpected error: %v", err)
	}
}

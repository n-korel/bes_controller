package main

import (
	"context"
	"os"
	"strings"
	"testing"

	app "bucis-bes_simulator/internal/app/bes"
	"bucis-bes_simulator/internal/infra/config"
)

func TestRun_ParsesPressFlag_AndPassesOptions(t *testing.T) {
	oldArgs := append([]string(nil), os.Args...)
	oldNotify := notifyContext
	oldAppRun := appRun
	oldStdin := stdin
	t.Cleanup(func() {
		os.Args = oldArgs
		notifyContext = oldNotify
		appRun = oldAppRun
		stdin = oldStdin
	})

	os.Args = []string{"bes", "-press"}
	stdin = strings.NewReader("")

	notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}

	// Минимальные env для ParseBes().
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_LISTEN_PORT_8890", "8890")
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", "6710")
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", "7777")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "3")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "2s")
	t.Setenv("EC_KEEPALIVE_TIMEOUT", "6s")

	appRun = func(ctx context.Context, _ app.Logger, cfg config.Bes, opts app.Options) error {
		if cfg.EC.ListenPort8890 != 8890 {
			t.Fatalf("unexpected cfg listen port: %d", cfg.EC.ListenPort8890)
		}
		if !opts.AutoPressAfterFirstReset {
			t.Fatalf("expected AutoPressAfterFirstReset=true")
		}
		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}

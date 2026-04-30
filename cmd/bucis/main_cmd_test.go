package main

import (
	"context"
	"os"
	"testing"
	"time"

	app "bucis-bes_simulator/internal/app/bucis"
)

func TestRun_ParsesFlags_AndPassesOptions(t *testing.T) {
	oldArgs := append([]string(nil), os.Args...)
	oldNotify := notifyContext
	oldAppRun := appRun
	t.Cleanup(func() {
		os.Args = oldArgs
		notifyContext = oldNotify
		appRun = oldAppRun
	})

	os.Args = []string{
		"bucis",
		"-ec-bes-broadcast-addr", "127.0.0.1",
		"-ec-bes-addr", "10.0.0.2",
		"-ec-opensips-ip", "1.2.3.4",
		"-ec-reset-interval", "3s",
		"-ec-keepalive-interval", "1s",
		"-ec-head1-ip", "5.5.5.5",
		"-ec-head2-ip", "6.6.6.6",
	}

	notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}

	appRun = func(ctx context.Context, _ app.Logger, opts app.Options) error {
		if opts.BesBroadcastAddr != "127.0.0.1" {
			t.Fatalf("BesBroadcastAddr=%q", opts.BesBroadcastAddr)
		}
		if opts.BesAddrOverride != "10.0.0.2" {
			t.Fatalf("BesAddrOverride=%q", opts.BesAddrOverride)
		}
		if opts.OpenSIPSIP != "1.2.3.4" {
			t.Fatalf("OpenSIPSIP=%q", opts.OpenSIPSIP)
		}
		if opts.ClientResetInterval != 3*time.Second {
			t.Fatalf("ClientResetInterval=%s", opts.ClientResetInterval)
		}
		if opts.KeepAliveInterval != 1*time.Second {
			t.Fatalf("KeepAliveInterval=%s", opts.KeepAliveInterval)
		}
		if opts.Head1IP != "5.5.5.5" {
			t.Fatalf("Head1IP=%q", opts.Head1IP)
		}
		if opts.Head2IP != "6.6.6.6" {
			t.Fatalf("Head2IP=%q", opts.Head2IP)
		}
		if ctx == nil {
			t.Fatalf("expected non-nil ctx")
		}
		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}

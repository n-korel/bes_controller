package main

import (
	"flag"
	"testing"
	"time"

	"bucis-bes_simulator/internal/infra/config"
)

func TestApplyECFlagOverrides_FlagsWin(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var (
		bcast    string
		override string
		resetInt time.Duration
		kaInt    time.Duration
	)
	fs.StringVar(&bcast, "ec-bes-broadcast-addr", "", "")
	fs.StringVar(&override, "ec-bes-addr", "", "")
	fs.DurationVar(&resetInt, "ec-reset-interval", 0, "")
	fs.DurationVar(&kaInt, "ec-keepalive-interval", 0, "")

	cfg := config.Bucis{EC: config.EC{
		BesBroadcastAddr:    "1.1.1.1",
		BesAddrOverride:     "2.2.2.2",
		ClientResetInterval: 5 * time.Second,
		KeepAliveInterval:   2 * time.Second,
	}}

	if err := fs.Parse([]string{
		"-ec-bes-broadcast-addr=127.0.0.1",
		"-ec-keepalive-interval=250ms",
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	applyECFlagOverrides(fs, &cfg, bcast, override, resetInt, kaInt)

	if got, want := cfg.EC.BesBroadcastAddr, "127.0.0.1"; got != want {
		t.Fatalf("BesBroadcastAddr=%q want %q", got, want)
	}
	// Не трогали override/reset → остаются прежними.
	if got, want := cfg.EC.BesAddrOverride, "2.2.2.2"; got != want {
		t.Fatalf("BesAddrOverride=%q want %q", got, want)
	}
	if got, want := cfg.EC.ClientResetInterval, 5*time.Second; got != want {
		t.Fatalf("ClientResetInterval=%s want %s", got, want)
	}
	if got, want := cfg.EC.KeepAliveInterval, 250*time.Millisecond; got != want {
		t.Fatalf("KeepAliveInterval=%s want %s", got, want)
	}
}

func TestChooseECdst_OverrideWins(t *testing.T) {
	if got, want := chooseECdst("bcast", "override"), "override"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := chooseECdst("bcast", ""), "bcast"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}


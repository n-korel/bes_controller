package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "bucis-bes_simulator/internal/app/bucis"
	"bucis-bes_simulator/pkg/log"
)

var (
	notifyContext = signal.NotifyContext
	appRun        = app.Run
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bucis")

	var (
		besAddrOverride string
		besBroadcast    string
		opensipsIP      string
		resetInterval   time.Duration
		keepAliveInt    time.Duration

		head1 string
		head2 string
	)

	ctx, stop := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&besBroadcast, "ec-bes-broadcast-addr", "", "EC destination broadcast address for reset/keepalive (default from EC_BES_BROADCAST_ADDR)")
	fs.StringVar(&besAddrOverride, "ec-bes-addr", "", "EC destination override for reset/keepalive (unicast) (default from EC_BES_ADDR)")
	fs.StringVar(&opensipsIP, "ec-opensips-ip", "", "OpenSIPS IP to put into keepalive/client_answer NewIP (default from SIP_DOMAIN, and if SIP_DOMAIN is empty — local IP)")
	fs.DurationVar(&resetInterval, "ec-reset-interval", 0, "ClientReset interval (default from EC_CLIENT_RESET_INTERVAL)")
	fs.DurationVar(&keepAliveInt, "ec-keepalive-interval", 0, "KeepAlive interval (default from EC_KEEPALIVE_INTERVAL)")
	fs.StringVar(&head1, "ec-head1-ip", "", "ClientReset head1 IP (default: local IP)")
	fs.StringVar(&head2, "ec-head2-ip", "", "ClientReset head2 IP (default: head1)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if err := appRun(ctx, logger, app.Options{
		BesBroadcastAddr:    besBroadcast,
		BesAddrOverride:     besAddrOverride,
		OpenSIPSIP:          opensipsIP,
		ClientResetInterval: resetInterval,
		KeepAliveInterval:   keepAliveInt,
		Head1IP:             head1,
		Head2IP:             head2,
	}); err != nil {
		return fmt.Errorf("bucis run: %w", err)
	}
	return nil
}

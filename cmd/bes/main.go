package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	app "bucis-bes_simulator/internal/app/bes"
	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/pkg/log"
)

var (
	notifyContext           = signal.NotifyContext
	appRun                  = app.Run
	stdin         io.Reader = os.Stdin
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pressFlag := fs.Bool("press", false, "send ClientQuery immediately after first ClientReset")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bes")

	ctx, stop := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.ParseBes()
	if err != nil {
		return fmt.Errorf("parse bes config: %w", err)
	}

	// Log the resolved keepalive settings to avoid confusion around EC_KEEPALIVE_TIMEOUT=0.
	// Note: config applies "timeout=0/unset => timeout=3*interval" and "-1 => disabled".
	{
		raw, ok := os.LookupEnv("EC_KEEPALIVE_TIMEOUT")
		raw = strings.TrimSpace(raw)
		timeoutExplicitlyZero := false
		timeoutExplicitlyDisabled := false
		timeoutWasUnset := !ok || raw == ""
		if ok && raw != "" {
			if raw == "-1" {
				timeoutExplicitlyDisabled = true
			} else if d, err := time.ParseDuration(raw); err == nil && d == 0 {
				timeoutExplicitlyZero = true
			}
		}

		logger.Info("ec keepalive config",
			"interval", cfg.EC.KeepAliveInterval.String(),
			"timeout", cfg.EC.KeepAliveTimeout.String(),
			"timeout_env", raw,
		)
		if timeoutExplicitlyDisabled || cfg.EC.KeepAliveTimeout < 0 {
			logger.Info("ec keepalive monitoring disabled", "rule", "EC_KEEPALIVE_TIMEOUT=-1")
		} else if timeoutWasUnset || timeoutExplicitlyZero {
			logger.Info("ec keepalive timeout resolved from interval",
				"timeout", cfg.EC.KeepAliveTimeout.String(),
				"interval", cfg.EC.KeepAliveInterval.String(),
				"rule", "timeout=3*interval when EC_KEEPALIVE_TIMEOUT=0 or unset",
			)
		}
	}

	mac := cfg.MAC

	// Reader for external "press" command (one line: "press").
	// This is the trigger for REGISTRATION_IDLE -> REGISTRATION_QUERYING.
	pressCh := make(chan struct{}, 1)
	r := stdin
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "press" {
				continue
			}
			select {
			case pressCh <- struct{}{}:
			default:
				// "press" during active querying/call is ignored (buffer is full).
			}
		}
	}()

	if err := appRun(ctx, logger, cfg, app.Options{
		MAC:                      mac,
		PressCh:                  pressCh,
		AutoPressAfterFirstReset: *pressFlag,
	}); err != nil {
		return fmt.Errorf("bes run: %w", err)
	}
	return nil
}

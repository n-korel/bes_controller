package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	app "bucis-bes_simulator/internal/app/bes"
	"bucis-bes_simulator/internal/infra/config"
	"bucis-bes_simulator/pkg/log"
)

func main() {
	pressFlag := flag.Bool("press", false, "send ClientQuery immediately after first ClientReset")
	flag.Parse()

	log.Init(os.Getenv("LOG_FORMAT"))
	logger := log.With("role", "bes")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.ParseBes()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Log the resolved keepalive settings to avoid confusion around EC_KEEPALIVE_TIMEOUT=0.
	// Note: config applies "timeout=0 => timeout=3*interval".
	{
		raw, ok := os.LookupEnv("EC_KEEPALIVE_TIMEOUT")
		raw = strings.TrimSpace(raw)
		timeoutExplicitlyZero := false
		timeoutWasUnset := !ok || raw == ""
		if ok && raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d == 0 {
				timeoutExplicitlyZero = true
			}
		}

		logger.Info("ec keepalive config",
			"interval", cfg.EC.KeepAliveInterval.String(),
			"timeout", cfg.EC.KeepAliveTimeout.String(),
			"timeout_env", raw,
		)
		if timeoutWasUnset || timeoutExplicitlyZero {
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
	go func() {
		sc := bufio.NewScanner(os.Stdin)
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

	if err := app.Run(ctx, logger, cfg, app.Options{
		MAC:                      mac,
		PressCh:                  pressCh,
		AutoPressAfterFirstReset: *pressFlag,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

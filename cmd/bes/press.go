package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

type answerEvent struct {
	answer *protocol.ClientAnswer
	from   *net.UDPAddr
}

func doPress(
	ctx context.Context,
	logger interface {
		Info(string, ...any)
		Warn(string, ...any)
	},
	cfg config.Bes,
	sendQueryOnce func() error,
	answers <-chan answerEvent,
) (sipID string, newIP string, err error) {
	for attempt := 1; attempt <= cfg.EC.ClientQueryMaxRetries; attempt++ {
		if err := sendQueryOnce(); err != nil {
			logger.Warn("ec_client_query send failed", "err", err, "attempt", attempt)
		} else {
			logger.Info("ec_client_query sent", "attempt", attempt, "to", cfg.EC.BucisAddr, "port", cfg.EC.QueryPort6710)
		}

		timeout := time.NewTimer(cfg.EC.ClientAnswerTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return "", "", ctx.Err()
		case ev := <-answers:
			timeout.Stop()
			if ev.answer == nil {
				return "", "", fmt.Errorf("received nil ec_client_answer")
			}
			fromIP := ""
			if ev.from != nil && ev.from.IP != nil {
				fromIP = ev.from.IP.String()
			}
			logger.Info("ec_client_answer received", "sip_id", ev.answer.SipID, "new_ip", ev.answer.NewIP, "from", fromIP)
			return ev.answer.SipID, ev.answer.NewIP, nil
		case <-timeout.C:
			// retry
		}

		if attempt < cfg.EC.ClientQueryMaxRetries {
			timer := time.NewTimer(cfg.EC.ClientQueryRetryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	return "", "", fmt.Errorf("no ec_client_answer after %d retries", cfg.EC.ClientQueryMaxRetries)
}

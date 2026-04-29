package main

import (
	"context"

	app "bucis-bes_simulator/internal/app/bes"
	"bucis-bes_simulator/internal/infra/config"
)

func runSIP(
	ctx context.Context,
	logger besLogger,
	cfg config.Bes,
	domain string,
	sipID string,
	conversations <-chan string,
	dialEstablished chan<- struct{},
) error {
	return app.DefaultRunSIP(ctx, logger, cfg, domain, sipID, conversations, dialEstablished)
}


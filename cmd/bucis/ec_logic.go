package main

import (
	"flag"
	"time"

	"bucis-bes_simulator/internal/infra/config"
)

func applyECFlagOverrides(
	fs *flag.FlagSet,
	cfg *config.Bucis,
	besBroadcast string,
	besAddrOverride string,
	resetInterval time.Duration,
	keepAliveInt time.Duration,
) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ec-bes-broadcast-addr":
			cfg.EC.BesBroadcastAddr = besBroadcast
		case "ec-bes-addr":
			cfg.EC.BesAddrOverride = besAddrOverride
		case "ec-reset-interval":
			cfg.EC.ClientResetInterval = resetInterval
		case "ec-keepalive-interval":
			cfg.EC.KeepAliveInterval = keepAliveInt
		}
	})
}

func chooseECdst(broadcastAddr, overrideAddr string) string {
	if overrideAddr != "" {
		return overrideAddr
	}
	return broadcastAddr
}


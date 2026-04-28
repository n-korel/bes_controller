package main

import (
	"flag"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"bucis-bes_simulator/internal/infra/config"
)

type sipIDState struct {
	mu  sync.Mutex
	seq int
	m   map[string]string // mac -> sipId
	ip  map[string]string // sipId -> bes ip
}

func newSipIDState() sipIDState {
	return sipIDState{m: make(map[string]string), ip: make(map[string]string)}
}

func (s *sipIDState) getSipID(mac string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[mac]; ok {
		return v
	}
	s.seq++
	v := strconv.Itoa(s.seq)
	s.m[mac] = v
	return v
}

func (s *sipIDState) setSipIDIP(sipID string, besIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sipID == "" || besIP == "" {
		return
	}
	s.ip[sipID] = besIP
}

func (s *sipIDState) getSipIDIP(sipID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ip[sipID]
	return v, ok
}

func (s *sipIDState) getSipIDByIP(besIP string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sipID, ip := range s.ip {
		if ip == besIP {
			return sipID, true
		}
	}
	return "", false
}

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

func enableUDPBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}


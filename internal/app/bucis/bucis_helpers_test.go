package bucis

import (
	"net"
	"testing"
	"time"

	"bucis-bes_simulator/internal/infra/config"
)

func TestChooseECdst(t *testing.T) {
	if got := chooseECdst("192.168.1.255", ""); got != "192.168.1.255" {
		t.Fatalf("chooseECdst(bcast, empty)=%q", got)
	}
	if got := chooseECdst("192.168.1.255", "10.0.0.1"); got != "10.0.0.1" {
		t.Fatalf("chooseECdst(bcast, override)=%q", got)
	}
}

func TestApplyECOverrides(t *testing.T) {
	cfg := &config.Bucis{EC: config.EC{
		BesBroadcastAddr:    "192.168.5.255",
		BesAddrOverride:     "",
		ClientResetInterval: 5 * time.Second,
		KeepAliveInterval:   2 * time.Second,
	}}
	applyECOverrides(cfg, Options{
		BesBroadcastAddr:    " 127.0.0.1 ",
		BesAddrOverride:     " 10.0.0.2 ",
		ClientResetInterval: 3 * time.Second,
		KeepAliveInterval:   1 * time.Second,
	})
	if cfg.EC.BesBroadcastAddr != "127.0.0.1" {
		t.Fatalf("BesBroadcastAddr=%q", cfg.EC.BesBroadcastAddr)
	}
	if cfg.EC.BesAddrOverride != "10.0.0.2" {
		t.Fatalf("BesAddrOverride=%q", cfg.EC.BesAddrOverride)
	}
	if cfg.EC.ClientResetInterval != 3*time.Second {
		t.Fatalf("ClientResetInterval=%s", cfg.EC.ClientResetInterval)
	}
	if cfg.EC.KeepAliveInterval != 1*time.Second {
		t.Fatalf("KeepAliveInterval=%s", cfg.EC.KeepAliveInterval)
	}
}

func TestIsIPv4LimitedBroadcast(t *testing.T) {
	if !isIPv4LimitedBroadcast(net.IPv4bcast) {
		t.Fatal("expected net.IPv4bcast to be limited broadcast")
	}
	if isIPv4LimitedBroadcast(net.IPv4(127, 0, 0, 1)) {
		t.Fatal("expected 127.0.0.1 to not be limited broadcast")
	}
	if isIPv4LimitedBroadcast(nil) {
		t.Fatal("expected nil to not be limited broadcast")
	}
}

func TestNormalizeSIPID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "  ", want: ""},
		{in: "bes_1", want: "1"},
		{in: " bes_001 ", want: "001"},
		{in: "bEs_1", want: "bEs_1"}, // case-sensitive by design
		{in: "123", want: "123"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := normalizeSIPID(tc.in); got != tc.want {
				t.Fatalf("normalizeSIPID(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHostFromSIPSource(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: " 10.0.0.1 ", want: "10.0.0.1"},
		{in: "10.0.0.1:5060", want: "10.0.0.1"},
		{in: "[::1]:5060", want: "::1"},
		{in: "example.com:1234", want: "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := hostFromSIPSource(tc.in); got != tc.want {
				t.Fatalf("hostFromSIPSource(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

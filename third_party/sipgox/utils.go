package sipgox

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/emiago/sipgo/sip"
)

// We are lazy to write full sip uris
func CheckLazySipUri(target string, destOverwrite string) string {
	if !strings.Contains(target, "@") {
		target = target + "@" + destOverwrite
	}

	if !strings.HasPrefix(target, "sip") {
		target = "sip:" + target
	}

	return target
}

func resolveHostIPWithTarget(network string, targetAddr string) (net.IP, error) {
	tip, _, _ := sip.ParseAddr(targetAddr)
	ip := net.ParseIP(tip)

	if network == "udp" {
		if ip != nil {
			if ip.IsLoopback() {
				// TO avoid UDP COnnected connection problem hitting different subnet
				return net.ParseIP("127.0.0.99"), nil
			}
		}
	}
	rip, _, err := sip.ResolveInterfacesIP("ip4", nil)
	return rip, err
}

func FindFreeInterfaceHostPort(network string, targetAddr string) (ip net.IP, port int, err error) {
	ip, err = resolveHostIPWithTarget(network, targetAddr)
	if err != nil {
		return ip, port, err
	}

	port, err = findFreePort(network, ip)
	if err != nil {
		return
	}

	if port == 0 {
		return ip, port, fmt.Errorf("failed to find free port")
	}

	return ip, port, err
}

func findFreePort(network string, ip net.IP) (int, error) {
	switch network {
	case "udp":
		var l *net.UDPConn
		l, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip})
		if err != nil {
			return 0, err
		}
		l.Close()
		port := l.LocalAddr().(*net.UDPAddr).Port
		return port, nil

	case "tcp", "ws", "tls", "wss":
		var l *net.TCPListener
		l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: ip})
		if err != nil {
			return 0, err
		}
		defer l.Close()
		port := l.Addr().(*net.TCPAddr).Port
		return port, nil
	default:
		return rand.Intn(9999) + 6000, nil
	}
}

func parsePortRange(raw string) (min int, max int, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}
	if !strings.Contains(raw, "-") {
		p, err := strconv.Atoi(raw)
		if err != nil || p <= 0 || p > 65535 {
			return 0, 0, false
		}
		return p, p, true
	}
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	b, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if a <= 0 || b <= 0 || a > 65535 || b > 65535 || a > b {
		return 0, 0, false
	}
	return a, b, true
}

func pickRTPPort(ip net.IP) int {
	min, max, ok := parsePortRange(os.Getenv("RTP_PORT_RANGE"))
	if !ok {
		min, max = 5003, 5009
	}
	for p := min; p <= max; p++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: p})
		if err != nil {
			continue
		}
		_ = c.Close()
		return p
	}
	return 0
}

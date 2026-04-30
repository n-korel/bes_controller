package bucis

import (
	"net"
	"testing"
)

func TestValidateConfigForTests_OK(t *testing.T) {
	// Минимально достаточные переменные окружения для ParseBucis().
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_LISTEN_PORT_8890", "8890")
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", "6710")
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", "7777")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "3")

	if err := ValidateConfigForTests(); err != nil {
		t.Fatalf("ValidateConfigForTests: %v", err)
	}
}

func TestFirstNonLoopbackIPv4_DoesNotPanic(t *testing.T) {
	ip, ok := firstNonLoopbackIPv4()
	if !ok {
		return
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		t.Fatalf("firstNonLoopbackIPv4 returned non-ipv4: %q", ip)
	}
}

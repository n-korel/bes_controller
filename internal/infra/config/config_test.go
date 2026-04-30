package config

import (
	"os"
	"testing"
	"time"
)

func resetDotEnv(t *testing.T) {
	t.Helper()
	resetDotEnvForTest()
	t.Cleanup(resetDotEnvForTest)
}

func TestMain(m *testing.M) {
	code := func() int {
		dir, err := os.MkdirTemp("", "config_test")
		if err != nil {
			panic(err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		if err := os.Chdir(dir); err != nil {
			panic(err)
		}
		return m.Run()
	}()
	os.Exit(code)
}

func TestParseBrs_Defaults(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_LISTEN_PORT_8890", "")
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", "")
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", "")
	t.Setenv("EC_BES_QUERY_DST_PORT", "")
	t.Setenv("EC_BUCIS_ADDR", "")
	t.Setenv("EC_BES_BROADCAST_ADDR", "")
	t.Setenv("EC_BES_ADDR", "")
	t.Setenv("EC_CLIENT_RESET_INTERVAL", "")
	t.Setenv("EC_KEEPALIVE_INTERVAL", "")
	t.Setenv("EC_CLIENT_ANSWER_TIMEOUT", "")
	t.Setenv("EC_CLIENT_QUERY_RETRY_INTERVAL", "")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "")

	cfg, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if cfg.EC.ListenPort8890 != 8890 || cfg.EC.QueryPort6710 != 6710 || cfg.EC.QueryPort7777 != 7777 || cfg.EC.BesQueryDstPort != 6710 {
		t.Fatalf("ports: listen8890=%d q6710=%d q7777=%d besQueryDst=%d", cfg.EC.ListenPort8890, cfg.EC.QueryPort6710, cfg.EC.QueryPort7777, cfg.EC.BesQueryDstPort)
	}
	if cfg.EC.BucisAddr != "127.0.0.1" {
		t.Fatalf("BucisAddr=%q", cfg.EC.BucisAddr)
	}
	if cfg.EC.BesBroadcastAddr != "192.168.5.255" {
		t.Fatalf("BesBroadcastAddr=%q", cfg.EC.BesBroadcastAddr)
	}
	if cfg.EC.ClientQueryMaxRetries != 3 {
		t.Fatalf("ClientQueryMaxRetries=%d", cfg.EC.ClientQueryMaxRetries)
	}
}

func TestParseBrs_CustomIntInvalid(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "x")

	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBucis_OK(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_LISTEN_PORT_8890", "8890")
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", "6710")
	t.Setenv("EC_BUCIS_QUERY_PORT_7777", "7777")
	t.Setenv("EC_BUCIS_ADDR", "127.0.0.1")
	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "3")

	cfg, err := ParseBucis()
	if err != nil {
		t.Fatalf("ParseBucis: %v", err)
	}
	if cfg.EC.BucisAddr != "127.0.0.1" || cfg.EC.ListenPort8890 != 8890 {
		t.Fatalf("got %#v", cfg)
	}
}

func TestParseBucis_RequiredMissing(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_BES_BROADCAST_ADDR", "not-an-ip")

	_, err := ParseBucis()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_InvalidBroadcastAddr(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_BES_BROADCAST_ADDR", "not-an-ip")
	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_KeepAliveTimeout_DefaultsTo3xInterval(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_KEEPALIVE_INTERVAL", "2s")
	t.Setenv("EC_KEEPALIVE_TIMEOUT", "")

	cfg, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if got, want := cfg.EC.KeepAliveTimeout, 6*time.Second; got != want {
		t.Fatalf("KeepAliveTimeout=%s want %s", got, want)
	}
}

func TestParseBes_KeepAliveTimeout_CanDisableMonitoringWithMinusOne(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_KEEPALIVE_INTERVAL", "2s")
	t.Setenv("EC_KEEPALIVE_TIMEOUT", "-1")

	cfg, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if got, want := cfg.EC.KeepAliveTimeout, time.Duration(-1); got != want {
		t.Fatalf("KeepAliveTimeout=%s want %s", got, want)
	}
}

func TestParseBes_MaxRetriesMustBePositive(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_CLIENT_QUERY_MAX_RETRIES", "0")
	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_InvalidDurationFails(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_KEEPALIVE_INTERVAL", "nope")
	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_InvalidIntFails(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_LISTEN_PORT_8890", "bad")
	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_InvalidKeepAliveTimeoutFails(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_KEEPALIVE_TIMEOUT", "bad")
	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBes_DotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	resetDotEnv(t)

	// В .env поставим "плохой" broadcast IP, который сломал бы парсинг,
	// но env должен иметь приоритет и предотвратить эту ошибку.
	if err := os.WriteFile(".env", []byte("EC_BES_BROADCAST_ADDR=not-an-ip\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(".env") })

	t.Setenv("EC_BES_BROADCAST_ADDR", "127.0.0.1")

	_, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
}

func TestParseBes_DotEnvInvalidLineFails(t *testing.T) {
	resetDotEnv(t)

	if err := os.WriteFile(".env", []byte("NO_EQUALS_HERE\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(".env") })

	_, err := ParseBes()
	if err == nil {
		t.Fatal("expected error")
	}
}

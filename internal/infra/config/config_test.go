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

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		raw     string
		wantMin int
		wantMax int
		wantOK  bool
	}{
		{raw: "", wantOK: false},
		{raw: " ", wantOK: false},
		{raw: "0", wantOK: false},
		{raw: "65536", wantOK: false},
		{raw: "123", wantMin: 123, wantMax: 123, wantOK: true},
		{raw: "1-2", wantMin: 1, wantMax: 2, wantOK: true},
		{raw: " 10 - 20 ", wantMin: 10, wantMax: 20, wantOK: true},
		{raw: "20-10", wantOK: false},
		{raw: "1-65535", wantMin: 1, wantMax: 65535, wantOK: true},
		{raw: "1-65536", wantOK: false},
		{raw: "1-2-3", wantOK: false},
		{raw: "abc", wantOK: false},
		{raw: "1-abc", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			min, max, ok := parsePortRange(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (min=%d max=%d)", ok, tc.wantOK, min, max)
			}
			if !tc.wantOK {
				return
			}
			if min != tc.wantMin || max != tc.wantMax {
				t.Fatalf("min=%d max=%d want %d-%d", min, max, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestValidateRTPPortRangeEnv(t *testing.T) {
	t.Run("unset_ok", func(t *testing.T) {
		t.Setenv("RTP_PORT_RANGE", "")
		if err := validateRTPPortRangeEnv(); err != nil {
			t.Fatalf("validateRTPPortRangeEnv: %v", err)
		}
	})
	t.Run("single_port_ok", func(t *testing.T) {
		t.Setenv("RTP_PORT_RANGE", "40000")
		if err := validateRTPPortRangeEnv(); err != nil {
			t.Fatalf("validateRTPPortRangeEnv: %v", err)
		}
	})
	t.Run("range_ok", func(t *testing.T) {
		t.Setenv("RTP_PORT_RANGE", "40000-40100")
		if err := validateRTPPortRangeEnv(); err != nil {
			t.Fatalf("validateRTPPortRangeEnv: %v", err)
		}
	})
	t.Run("invalid_fails", func(t *testing.T) {
		t.Setenv("RTP_PORT_RANGE", "bad")
		if err := validateRTPPortRangeEnv(); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("out_of_bounds_fails", func(t *testing.T) {
		t.Setenv("RTP_PORT_RANGE", "0-10")
		if err := validateRTPPortRangeEnv(); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestGetEnvUint64Default(t *testing.T) {
	t.Run("unset_returns_default", func(t *testing.T) {
		t.Setenv("UINT64_KEY", "")
		got, err := getEnvUint64Default("UINT64_KEY", 42)
		if err != nil {
			t.Fatalf("getEnvUint64Default: %v", err)
		}
		if got != 42 {
			t.Fatalf("got=%d want %d", got, 42)
		}
	})
	t.Run("valid_parses_and_trims", func(t *testing.T) {
		t.Setenv("UINT64_KEY", "  184467  ")
		got, err := getEnvUint64Default("UINT64_KEY", 0)
		if err != nil {
			t.Fatalf("getEnvUint64Default: %v", err)
		}
		if got != 184467 {
			t.Fatalf("got=%d want %d", got, 184467)
		}
	})
	t.Run("invalid_fails", func(t *testing.T) {
		t.Setenv("UINT64_KEY", "nope")
		_, err := getEnvUint64Default("UINT64_KEY", 0)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseBes_BesQueryDstPort_DefaultDoesNotFollowBucis6710(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_BUCIS_QUERY_PORT_6710", "7000")
	t.Setenv("EC_BES_QUERY_DST_PORT", "")

	cfg, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if cfg.EC.QueryPort6710 != 7000 {
		t.Fatalf("QueryPort6710=%d want 7000", cfg.EC.QueryPort6710)
	}
	if cfg.EC.BesQueryDstPort != 6710 {
		t.Fatalf("BesQueryDstPort=%d want 6710 (default must not track EC_BUCIS_QUERY_PORT_6710)", cfg.EC.BesQueryDstPort)
	}
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

func TestParseBes_QueryDstPort_OutOfRangeFails(t *testing.T) {
	resetDotEnv(t)

	t.Run("zero", func(t *testing.T) {
		t.Setenv("EC_BES_QUERY_DST_PORT", "0")
		_, err := ParseBes()
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("too_large", func(t *testing.T) {
		t.Setenv("EC_BES_QUERY_DST_PORT", "65536")
		_, err := ParseBes()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseBes_SipIDModulo_OneFails(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_SIPID_MODULO", "1")

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

func TestParseBes_ConversationTimeout_ParsesAndZeroMeansUnlimited(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_CONVERSATION_TIMEOUT", "45s")

	cfg, err := ParseBes()
	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if got, want := cfg.EC.ConversationTimeout, 45*time.Second; got != want {
		t.Fatalf("ConversationTimeout=%s want %s", got, want)
	}

	resetDotEnv(t)
	t.Setenv("EC_CONVERSATION_TIMEOUT", "")

	cfg2, err := ParseBes()

	if err != nil {
		t.Fatalf("ParseBes: %v", err)
	}
	if cfg2.EC.ConversationTimeout != 0 {
		t.Fatalf("ConversationTimeout=%s want 0", cfg2.EC.ConversationTimeout)
	}
}

func TestParseBes_ConversationTimeout_NegativeFails(t *testing.T) {
	resetDotEnv(t)
	t.Setenv("EC_CONVERSATION_TIMEOUT", "-1s")

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

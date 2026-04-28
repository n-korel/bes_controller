package main

import (
	"context"
	"strings"
	"testing"

	"bucis-bes_simulator/internal/infra/config"
)

func TestRunSIP_ValidationErrors(t *testing.T) {
	t.Run("invalid SIP_PORT", func(t *testing.T) {
		t.Setenv("SIP_PORT", "nope")
		t.Setenv("SIP_USER_BUCIS", "bucis")
		t.Setenv("SIP_USER_BES", "bes_1")
		err := runSIP(context.Background(), nopLogger{}, config.Bes{}, "127.0.0.1", "1", nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_PORT invalid") {
			t.Fatalf("expected SIP_PORT invalid error, got %v", err)
		}
	})

	t.Run("missing SIP_USER_BUCIS", func(t *testing.T) {
		t.Setenv("SIP_PORT", "5060")
		t.Setenv("SIP_USER_BUCIS", "")
		t.Setenv("SIP_USER_BES", "bes_1")
		err := runSIP(context.Background(), nopLogger{}, config.Bes{}, "127.0.0.1", "1", nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_USER_BUCIS must be set") {
			t.Fatalf("expected SIP_USER_BUCIS error, got %v", err)
		}
	})

	t.Run("missing SIP_USER_BES when sipID empty", func(t *testing.T) {
		t.Setenv("SIP_PORT", "5060")
		t.Setenv("SIP_USER_BUCIS", "bucis")
		t.Setenv("SIP_USER_BES", "")
		err := runSIP(context.Background(), nopLogger{}, config.Bes{}, "127.0.0.1", "", nil)
		if err == nil || !strings.Contains(err.Error(), "SIP_USER_BES must be set") {
			t.Fatalf("expected SIP_USER_BES error, got %v", err)
		}
	})
}

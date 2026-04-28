package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestOnClientReset_DoesNotInterruptCall(t *testing.T) {
	var st atomic.Uint32
	st.Store(stateInCall)
	onClientReset(&st)
	if got, want := st.Load(), uint32(stateInCall); got != want {
		t.Fatalf("state=%d want %d", got, want)
	}
}

func TestOnClientReset_MovesToRegistrationWhenNotInCall(t *testing.T) {
	var st atomic.Uint32
	st.Store(stateUnregistered)
	onClientReset(&st)
	if got, want := st.Load(), uint32(stateRegistrationIdle); got != want {
		t.Fatalf("state=%d want %d", got, want)
	}
}

func TestKeepaliveShouldWarn_BoundariesAndRateLimit(t *testing.T) {
	timeout := 2 * time.Second
	now := time.Unix(100, 0)

	// UNREGISTERED -> никогда не варним.
	if warn, _ := keepaliveShouldWarn(stateUnregistered, now.Add(-10*time.Second).UnixNano(), timeout, now, time.Time{}); warn {
		t.Fatalf("unexpected warn in UNREGISTERED")
	}

	// last==0 -> никогда не варним.
	if warn, _ := keepaliveShouldWarn(stateRegistrationIdle, 0, timeout, now, time.Time{}); warn {
		t.Fatalf("unexpected warn with last=0")
	}

	// age<=timeout -> no warn и warnedAt сбрасывается.
	warn, warnedAt := keepaliveShouldWarn(stateRegistrationIdle, now.Add(-1*time.Second).UnixNano(), timeout, now, now.Add(-10*time.Second))
	if warn {
		t.Fatalf("unexpected warn when age<=timeout")
	}
	if !warnedAt.IsZero() {
		t.Fatalf("warnedAt should reset to zero, got %v", warnedAt)
	}

	// age>timeout и warnedAt==zero -> warn.
	warn, warnedAt = keepaliveShouldWarn(stateRegistrationIdle, now.Add(-3*time.Second).UnixNano(), timeout, now, time.Time{})
	if !warn {
		t.Fatalf("expected warn")
	}
	if warnedAt != now {
		t.Fatalf("warnedAt=%v want %v", warnedAt, now)
	}

	// rate-limit: если уже варнили недавно (<timeout) -> не варним.
	warn, warnedAt = keepaliveShouldWarn(stateRegistrationIdle, now.Add(-3*time.Second).UnixNano(), timeout, now.Add(500*time.Millisecond), now)
	if warn {
		t.Fatalf("unexpected warn under rate-limit")
	}
	if warnedAt != now {
		t.Fatalf("warnedAt changed: %v", warnedAt)
	}

	// после timeout -> снова варним.
	later := now.Add(3 * time.Second)
	warn, warnedAt = keepaliveShouldWarn(stateRegistrationIdle, now.Add(-6*time.Second).UnixNano(), timeout, later, now)
	if !warn {
		t.Fatalf("expected warn after timeout window")
	}
	if warnedAt != later {
		t.Fatalf("warnedAt=%v want %v", warnedAt, later)
	}
}


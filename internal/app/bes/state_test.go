package bes

import (
	"fmt"
	"math"
	"testing"
	"time"
)

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

func TestKeepaliveShouldWarn_TimeoutZero_DoesNotPanicAndDoesNotWarnInUnregistered(t *testing.T) {
	now := time.Unix(100, 0)
	timeout := time.Duration(0)

	cases := []int64{
		0,
		1,
		-1,
		now.UnixNano(),
		now.Add(-10 * time.Second).UnixNano(),
		now.Add(10 * time.Second).UnixNano(),
		math.MinInt64,
		math.MaxInt64,
	}

	for _, last := range cases {
		t.Run(fmt.Sprintf("last=%d", last), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()

			if warn, _ := keepaliveShouldWarn(stateUnregistered, last, timeout, now, time.Time{}); warn {
				t.Fatalf("unexpected warn in UNREGISTERED with timeout=0, last=%d", last)
			}
		})
	}
}

func TestBES_KeepaliveWarning_NotTriggeredInUnregistered(t *testing.T) {
	timeout := 2 * time.Second
	now := time.Unix(100, 0)
	last := now.Add(-10 * time.Second).UnixNano()

	if warn, _ := keepaliveShouldWarn(stateUnregistered, last, timeout, now, time.Time{}); warn {
		t.Fatalf("unexpected warn before first ClientReset (UNREGISTERED)")
	}
}

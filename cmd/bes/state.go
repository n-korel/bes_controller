package main

import (
	"time"
)

const (
	stateUnregistered      uint32 = 0
	stateRegistrationIdle  uint32 = 1
	stateRegistrationQuery uint32 = 2
	// stateCallSetup exists to properly model ClientReset behavior during SIP setup.
	// If ClientReset arrives here, the SIP attempt must be aborted.
	stateCallSetup uint32 = 3
	stateInCall    uint32 = 4
)

func keepaliveShouldWarn(
	state uint32,
	lastKeepAliveUnixNano int64,
	timeout time.Duration,
	now time.Time,
	warnedAt time.Time,
) (warn bool, newWarnedAt time.Time) {
	if timeout <= 0 {
		return false, warnedAt
	}
	if state == stateUnregistered {
		return false, warnedAt
	}
	if lastKeepAliveUnixNano == 0 {
		return false, warnedAt
	}
	age := now.Sub(time.Unix(0, lastKeepAliveUnixNano))
	if age <= timeout {
		return false, time.Time{}
	}
	if warnedAt.IsZero() || now.Sub(warnedAt) >= timeout {
		return true, now
	}
	return false, warnedAt
}


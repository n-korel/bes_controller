package main

import (
	"sync/atomic"
	"time"
)

const (
	stateUnregistered      uint32 = 0
	stateRegistrationIdle  uint32 = 1
	stateRegistrationQuery uint32 = 2
	stateInCall            uint32 = 3
)

func onClientReset(state *atomic.Uint32) {
	// UNREGISTERED -> REGISTRATION (but don't interrupt an active call)
	if state.Load() != stateInCall {
		state.Store(stateRegistrationIdle)
	}
}

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


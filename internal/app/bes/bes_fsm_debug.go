//go:build debug

package bes

import "fmt"

func besPanicOnUnexpectedFSMState(st uint32) {
	panic(fmt.Sprintf("bes: unexpected FSM state %d", st))
}

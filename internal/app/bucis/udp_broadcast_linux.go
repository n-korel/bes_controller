//go:build linux

package bucis

import (
	"fmt"
	"net"
	"syscall"
)

func enableUDPBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("syscall conn: %w", err)
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return fmt.Errorf("raw control: %w", err)
	}
	if serr != nil {
		return fmt.Errorf("setsockopt(SO_BROADCAST): %w", serr)
	}
	return nil
}

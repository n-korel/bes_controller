//go:build windows

package bucis

import "net"

func enableUDPBroadcast(_ *net.UDPConn) error {
	return ErrBroadcastNotSupported
}

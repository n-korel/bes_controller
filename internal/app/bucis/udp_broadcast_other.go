//go:build !linux && !windows

package bucis

import "net"

func enableUDPBroadcast(_ *net.UDPConn) error {
	// На non-Linux ОС broadcast обычно работает без явного SO_BROADCAST,
	// а если нет — дадим упасть на Write с понятной сетевой ошибкой.
	return nil
}

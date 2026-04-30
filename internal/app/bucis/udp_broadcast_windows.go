//go:build windows

package bucis

import (
	"errors"
	"net"
)

func enableUDPBroadcast(_ *net.UDPConn) error {
	return errors.New("udp broadcast is not supported on windows build; configure unicast destination (see .env.bes.windows / EC_BES_ADDR_OVERRIDE)")
}


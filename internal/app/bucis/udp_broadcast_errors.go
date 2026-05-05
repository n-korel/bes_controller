package bucis

import "errors"

var ErrBroadcastNotSupported = errors.New("udp broadcast is not supported on windows build; configure unicast destination (see .env.bes.windows / EC_BES_ADDR_OVERRIDE)")

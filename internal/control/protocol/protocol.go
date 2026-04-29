package protocol

import (
	"strings"
)

type ECPacketType uint8

const (
	ECPacketUnknown ECPacketType = iota
	ECPacketClientReset
	ECPacketClientQuery
	ECPacketClientAnswer
	ECPacketServerKeepAlive
	ECPacketClientConversation
)

type ECPacket struct {
	Type ECPacketType

	Reset        *ClientReset
	Query        *ClientQuery
	Answer       *ClientAnswer
	KeepAlive    *KeepAlive
	Conversation *ClientConversation
}

type ClientReset struct {
	IPHead1 string
	IPHead2 string
}

type ClientQuery struct {
	MAC string
}

type ClientAnswer struct {
	NewIP string
	SipID string
}

type KeepAlive struct {
	OpenSIPSIP string
	Status    string
}

type ClientConversation struct {
	SipID string
}

func Parse(b []byte) (pkt ECPacket, ok bool) {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ECPacket{}, false
	}

	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ECPacket{}, false
	}
	cmd := fields[0]
	rest := strings.TrimSpace(s[len(cmd):])

	switch cmd {
	case "ec_client_reset":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return ECPacket{}, false
		}
		if len(parts) != 2 {
			return ECPacket{}, false
		}
		return ECPacket{Type: ECPacketClientReset, Reset: &ClientReset{IPHead1: parts[0], IPHead2: parts[1]}}, true

	case "ec_client_query":
		mac := strings.TrimSpace(rest)
		if mac == "" {
			return ECPacket{}, false
		}
		if strings.Contains(mac, ";") {
			return ECPacket{}, false
		}
		return ECPacket{Type: ECPacketClientQuery, Query: &ClientQuery{MAC: mac}}, true

	case "ec_client_answer":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return ECPacket{}, false
		}
		if len(parts) != 2 {
			return ECPacket{}, false
		}
		return ECPacket{Type: ECPacketClientAnswer, Answer: &ClientAnswer{NewIP: parts[0], SipID: parts[1]}}, true

	case "ec_server_keepalive":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return ECPacket{}, false
		}
		if len(parts) != 2 {
			return ECPacket{}, false
		}
		return ECPacket{Type: ECPacketServerKeepAlive, KeepAlive: &KeepAlive{OpenSIPSIP: parts[0], Status: parts[1]}}, true

	case "ec_client_conversation":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return ECPacket{}, false
		}
		if len(parts) != 1 {
			return ECPacket{}, false
		}
		return ECPacket{Type: ECPacketClientConversation, Conversation: &ClientConversation{SipID: parts[0]}}, true

	default:
		return ECPacket{}, false
	}
}

func FormatClientReset(ipHead1, ipHead2 string) string {
	return "ec_client_reset " + strings.TrimSpace(ipHead1) + ";" + strings.TrimSpace(ipHead2) + ";"
}

func FormatClientQuery(mac string) string {
	return "ec_client_query " + strings.TrimSpace(mac)
}

func FormatClientAnswer(newIP, sipID string) string {
	return "ec_client_answer " + strings.TrimSpace(newIP) + ";" + strings.TrimSpace(sipID) + ";"
}

func FormatKeepAlive(opensipsIP, status string) string {
	return "ec_server_keepalive " + strings.TrimSpace(opensipsIP) + ";" + strings.TrimSpace(status) + ";"
}

func FormatClientConversation(sipID string) string {
	return "ec_client_conversation " + strings.TrimSpace(sipID) + ";"
}

func splitNonEmptySemicolonParts(s string) []string {
	var parts []string
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func splitNonEmptySemicolonPartsWithTrailing(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, ";") {
		return nil, false
	}
	s = strings.TrimSuffix(s, ";")
	return splitNonEmptySemicolonParts(s), true
}

package protocol

import (
	"strings"
)

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

func Parse(b []byte) (reset *ClientReset, query *ClientQuery, answer *ClientAnswer, keepalive *KeepAlive, conv *ClientConversation, ok bool) {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil, nil, nil, nil, nil, false
	}

	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, nil, nil, nil, nil, false
	}
	cmd := fields[0]
	rest := strings.TrimSpace(s[len(cmd):])

	switch cmd {
	case "ec_client_reset":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return nil, nil, nil, nil, nil, false
		}
		if len(parts) != 2 {
			return nil, nil, nil, nil, nil, false
		}
		return &ClientReset{IPHead1: parts[0], IPHead2: parts[1]}, nil, nil, nil, nil, true

	case "ec_client_query":
		mac := strings.TrimSpace(rest)
		if mac == "" {
			return nil, nil, nil, nil, nil, false
		}
		if strings.Contains(mac, ";") {
			return nil, nil, nil, nil, nil, false
		}
		return nil, &ClientQuery{MAC: mac}, nil, nil, nil, true

	case "ec_client_answer":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return nil, nil, nil, nil, nil, false
		}
		if len(parts) != 2 {
			return nil, nil, nil, nil, nil, false
		}
		return nil, nil, &ClientAnswer{NewIP: parts[0], SipID: parts[1]}, nil, nil, true

	case "ec_server_keepalive":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return nil, nil, nil, nil, nil, false
		}
		if len(parts) != 2 {
			return nil, nil, nil, nil, nil, false
		}
		return nil, nil, nil, &KeepAlive{OpenSIPSIP: parts[0], Status: parts[1]}, nil, true

	case "ec_client_conversation":
		parts, ok := splitNonEmptySemicolonPartsWithTrailing(rest)
		if !ok {
			return nil, nil, nil, nil, nil, false
		}
		if len(parts) != 1 {
			return nil, nil, nil, nil, nil, false
		}
		return nil, nil, nil, nil, &ClientConversation{SipID: parts[0]}, true

	default:
		return nil, nil, nil, nil, nil, false
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

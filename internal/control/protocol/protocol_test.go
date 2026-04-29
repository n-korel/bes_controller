package protocol

import (
	"strings"
	"testing"
)

func TestParseClientReset(t *testing.T) {
	cases := []struct {
		line string
		want ClientReset
	}{
		{"ec_client_reset 192.168.5.251;192.168.5.252;", ClientReset{IPHead1: "192.168.5.251", IPHead2: "192.168.5.252"}},
		{" ec_client_reset   1.2.3.4 ; 5.6.7.8 ;  ", ClientReset{IPHead1: "1.2.3.4", IPHead2: "5.6.7.8"}},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.line, " ", "_"), func(t *testing.T) {
			pkt, ok := Parse([]byte(tc.line))
			if !ok || pkt.Type != ECPacketClientReset || pkt.Reset == nil || pkt.Query != nil || pkt.Answer != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
				t.Fatalf("Parse() ok=%v pkt=%+v", ok, pkt)
			}
			if *pkt.Reset != tc.want {
				t.Fatalf("got %+v want %+v", *pkt.Reset, tc.want)
			}
		})
	}
}

func TestParseClientQuery(t *testing.T) {
	line := "ec_client_query AA:BB:CC:DD:EE:FF"
	pkt, ok := Parse([]byte(line))
	if !ok || pkt.Type != ECPacketClientQuery || pkt.Query == nil || pkt.Reset != nil || pkt.Answer != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
		t.Fatalf("Parse() ok=%v pkt=%+v", ok, pkt)
	}
	if pkt.Query.MAC != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("got %+v", *pkt.Query)
	}
}

func TestParseClientAnswer(t *testing.T) {
	line := "ec_client_answer 192.168.5.251;1234;"
	pkt, ok := Parse([]byte(line))
	if !ok || pkt.Type != ECPacketClientAnswer || pkt.Answer == nil || pkt.Reset != nil || pkt.Query != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
		t.Fatalf("Parse() ok=%v pkt=%+v", ok, pkt)
	}
	if pkt.Answer.NewIP != "192.168.5.251" || pkt.Answer.SipID != "1234" {
		t.Fatalf("got %+v", *pkt.Answer)
	}
}

func TestParseKeepAlive(t *testing.T) {
	line := "ec_server_keepalive 192.168.5.251;0;"
	pkt, ok := Parse([]byte(line))
	if !ok || pkt.Type != ECPacketServerKeepAlive || pkt.KeepAlive == nil || pkt.Reset != nil || pkt.Query != nil || pkt.Answer != nil || pkt.Conversation != nil {
		t.Fatalf("Parse() ok=%v pkt=%+v", ok, pkt)
	}
	if pkt.KeepAlive.OpenSIPSIP != "192.168.5.251" || pkt.KeepAlive.Status != "0" {
		t.Fatalf("got %+v", *pkt.KeepAlive)
	}
}

func TestParseClientConversation(t *testing.T) {
	line := "ec_client_conversation 1234;"
	pkt, ok := Parse([]byte(line))
	if !ok || pkt.Type != ECPacketClientConversation || pkt.Conversation == nil || pkt.Reset != nil || pkt.Query != nil || pkt.Answer != nil || pkt.KeepAlive != nil {
		t.Fatalf("Parse() ok=%v pkt=%+v", ok, pkt)
	}
	if pkt.Conversation.SipID != "1234" {
		t.Fatalf("got %+v", *pkt.Conversation)
	}
}

func TestParseEmptyAndMalformed(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"ec_client_reset",
		"ec_client_reset 1.2.3.4;",
		"ec_client_reset 1.2.3.4;5.6.7.8",
		"ec_client_answer",
		"ec_client_answer 1.2.3.4;",
		"ec_client_answer ;123;",
		"ec_server_keepalive 1.2.3.4;",
		"ec_server_keepalive ;0;",
		"ec_client_conversation",
		"ec_client_conversation ;",
		"ec_client_query",
		"ec_client_query AA:BB:CC;",
		"unknown_cmd",
	}
	for _, line := range bad {
		t.Run(strings.ReplaceAll(line, " ", "_"), func(t *testing.T) {
			pkt, ok := Parse([]byte(line))
			if ok {
				t.Fatalf("expected failure, got pkt=%+v", pkt)
			}
		})
	}
}

func TestFormatters(t *testing.T) {
	if got, want := FormatClientReset("1.2.3.4", "5.6.7.8"), "ec_client_reset 1.2.3.4;5.6.7.8;"; got != want {
		t.Fatalf("FormatClientReset: got %q want %q", got, want)
	}
	if got, want := FormatClientQuery("AA:BB"), "ec_client_query AA:BB"; got != want {
		t.Fatalf("FormatClientQuery: got %q want %q", got, want)
	}
	if got, want := FormatClientAnswer("1.2.3.4", "123"), "ec_client_answer 1.2.3.4;123;"; got != want {
		t.Fatalf("FormatClientAnswer: got %q want %q", got, want)
	}
	if got, want := FormatKeepAlive("1.2.3.4", "0"), "ec_server_keepalive 1.2.3.4;0;"; got != want {
		t.Fatalf("FormatKeepAlive: got %q want %q", got, want)
	}
	if got, want := FormatClientConversation("123"), "ec_client_conversation 123;"; got != want {
		t.Fatalf("FormatClientConversation: got %q want %q", got, want)
	}
}

func TestFormatParse_RoundTrip(t *testing.T) {
	{
		line := FormatClientReset(" 1.2.3.4 ", " 5.6.7.8 ")
		pkt, ok := Parse([]byte(line))
		if !ok || pkt.Type != ECPacketClientReset || pkt.Reset == nil || pkt.Query != nil || pkt.Answer != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
			t.Fatalf("Parse(reset) ok=%v pkt=%+v", ok, pkt)
		}
		if pkt.Reset.IPHead1 != "1.2.3.4" || pkt.Reset.IPHead2 != "5.6.7.8" {
			t.Fatalf("reset=%+v", *pkt.Reset)
		}
	}
	{
		line := FormatClientQuery(" AA:BB:CC ")
		pkt, ok := Parse([]byte(line))
		if !ok || pkt.Type != ECPacketClientQuery || pkt.Query == nil || pkt.Reset != nil || pkt.Answer != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
			t.Fatalf("Parse(query) ok=%v pkt=%+v", ok, pkt)
		}
		if pkt.Query.MAC != "AA:BB:CC" {
			t.Fatalf("query=%+v", *pkt.Query)
		}
	}
	{
		line := FormatClientAnswer(" 1.2.3.4 ", " 123 ")
		pkt, ok := Parse([]byte(line))
		if !ok || pkt.Type != ECPacketClientAnswer || pkt.Answer == nil || pkt.Reset != nil || pkt.Query != nil || pkt.KeepAlive != nil || pkt.Conversation != nil {
			t.Fatalf("Parse(answer) ok=%v pkt=%+v", ok, pkt)
		}
		if pkt.Answer.NewIP != "1.2.3.4" || pkt.Answer.SipID != "123" {
			t.Fatalf("answer=%+v", *pkt.Answer)
		}
	}
	{
		line := FormatKeepAlive(" 1.2.3.4 ", " 0 ")
		pkt, ok := Parse([]byte(line))
		if !ok || pkt.Type != ECPacketServerKeepAlive || pkt.KeepAlive == nil || pkt.Reset != nil || pkt.Query != nil || pkt.Answer != nil || pkt.Conversation != nil {
			t.Fatalf("Parse(keepalive) ok=%v pkt=%+v", ok, pkt)
		}
		if pkt.KeepAlive.OpenSIPSIP != "1.2.3.4" || pkt.KeepAlive.Status != "0" {
			t.Fatalf("keepalive=%+v", *pkt.KeepAlive)
		}
	}
	{
		line := FormatClientConversation(" 123 ")
		pkt, ok := Parse([]byte(line))
		if !ok || pkt.Type != ECPacketClientConversation || pkt.Conversation == nil || pkt.Reset != nil || pkt.Query != nil || pkt.Answer != nil || pkt.KeepAlive != nil {
			t.Fatalf("Parse(conv) ok=%v pkt=%+v", ok, pkt)
		}
		if pkt.Conversation.SipID != "123" {
			t.Fatalf("conv=%+v", *pkt.Conversation)
		}
	}
}

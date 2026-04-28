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
			reset, query, answer, keepalive, conv, ok := Parse([]byte(tc.line))
			if !ok || query != nil || answer != nil || keepalive != nil || conv != nil {
				t.Fatalf("Parse() ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
			}
			if *reset != tc.want {
				t.Fatalf("got %+v want %+v", *reset, tc.want)
			}
		})
	}
}

func TestParseClientQuery(t *testing.T) {
	line := "ec_client_query AA:BB:CC:DD:EE:FF"
	reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
	if !ok || reset != nil || answer != nil || keepalive != nil || conv != nil {
		t.Fatalf("Parse() ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
	}
	if query.MAC != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("got %+v", *query)
	}
}

func TestParseClientAnswer(t *testing.T) {
	line := "ec_client_answer 192.168.5.251;1234;"
	reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
	if !ok || reset != nil || query != nil || keepalive != nil || conv != nil {
		t.Fatalf("Parse() ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
	}
	if answer.NewIP != "192.168.5.251" || answer.SipID != "1234" {
		t.Fatalf("got %+v", *answer)
	}
}

func TestParseKeepAlive(t *testing.T) {
	line := "ec_server_keepalive 192.168.5.251;0;"
	reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
	if !ok || reset != nil || query != nil || answer != nil || conv != nil {
		t.Fatalf("Parse() ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
	}
	if keepalive.OpenSIPSIP != "192.168.5.251" || keepalive.Status != "0" {
		t.Fatalf("got %+v", *keepalive)
	}
}

func TestParseClientConversation(t *testing.T) {
	line := "ec_client_conversation 1234;"
	reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
	if !ok || reset != nil || query != nil || answer != nil || keepalive != nil {
		t.Fatalf("Parse() ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
	}
	if conv.SipID != "1234" {
		t.Fatalf("got %+v", *conv)
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
			reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
			if ok {
				t.Fatalf("expected failure, got reset=%v query=%v answer=%v keepalive=%v conv=%v", reset, query, answer, keepalive, conv)
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
		reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
		if !ok || query != nil || answer != nil || keepalive != nil || conv != nil {
			t.Fatalf("Parse(reset) ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
		}
		if reset.IPHead1 != "1.2.3.4" || reset.IPHead2 != "5.6.7.8" {
			t.Fatalf("reset=%+v", *reset)
		}
	}
	{
		line := FormatClientQuery(" AA:BB:CC ")
		reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
		if !ok || reset != nil || answer != nil || keepalive != nil || conv != nil {
			t.Fatalf("Parse(query) ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
		}
		if query.MAC != "AA:BB:CC" {
			t.Fatalf("query=%+v", *query)
		}
	}
	{
		line := FormatClientAnswer(" 1.2.3.4 ", " 123 ")
		reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
		if !ok || reset != nil || query != nil || keepalive != nil || conv != nil {
			t.Fatalf("Parse(answer) ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
		}
		if answer.NewIP != "1.2.3.4" || answer.SipID != "123" {
			t.Fatalf("answer=%+v", *answer)
		}
	}
	{
		line := FormatKeepAlive(" 1.2.3.4 ", " 0 ")
		reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
		if !ok || reset != nil || query != nil || answer != nil || conv != nil {
			t.Fatalf("Parse(keepalive) ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
		}
		if keepalive.OpenSIPSIP != "1.2.3.4" || keepalive.Status != "0" {
			t.Fatalf("keepalive=%+v", *keepalive)
		}
	}
	{
		line := FormatClientConversation(" 123 ")
		reset, query, answer, keepalive, conv, ok := Parse([]byte(line))
		if !ok || reset != nil || query != nil || answer != nil || keepalive != nil {
			t.Fatalf("Parse(conv) ok=%v reset=%v query=%v answer=%v keepalive=%v conv=%v", ok, reset, query, answer, keepalive, conv)
		}
		if conv.SipID != "123" {
			t.Fatalf("conv=%+v", *conv)
		}
	}
}

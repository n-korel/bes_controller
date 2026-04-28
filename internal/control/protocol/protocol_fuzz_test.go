package protocol

import "testing"

func FuzzParse(f *testing.F) {
	seeds := [][]byte{
		[]byte(""),
		[]byte("   "),
		[]byte("unknown_cmd"),
		[]byte(FormatClientReset("1.2.3.4", "5.6.7.8")),
		[]byte(FormatClientQuery("AA:BB:CC:DD:EE:FF")),
		[]byte(FormatClientAnswer("1.2.3.4", "123")),
		[]byte(FormatKeepAlive("1.2.3.4", "0")),
		[]byte(FormatClientConversation("123")),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		reset, query, answer, keepalive, conv, ok := Parse(b)
		if !ok {
			return
		}
		if reset == nil && query == nil && answer == nil && keepalive == nil && conv == nil {
			t.Fatalf("ok=true but all messages are nil")
		}

		if reset != nil {
			rr, qq, aa, kk, cc, ok2 := Parse([]byte(FormatClientReset(reset.IPHead1, reset.IPHead2)))
			if !ok2 || rr == nil || qq != nil || aa != nil || kk != nil || cc != nil {
				t.Fatalf("roundtrip reset failed: ok=%v reset=%v", ok2, rr)
			}
		}
		if query != nil {
			rr, qq, aa, kk, cc, ok2 := Parse([]byte(FormatClientQuery(query.MAC)))
			if !ok2 || qq == nil || rr != nil || aa != nil || kk != nil || cc != nil {
				t.Fatalf("roundtrip query failed: ok=%v query=%v", ok2, qq)
			}
		}
		if answer != nil {
			rr, qq, aa, kk, cc, ok2 := Parse([]byte(FormatClientAnswer(answer.NewIP, answer.SipID)))
			if !ok2 || aa == nil || rr != nil || qq != nil || kk != nil || cc != nil {
				t.Fatalf("roundtrip answer failed: ok=%v answer=%v", ok2, aa)
			}
		}
		if keepalive != nil {
			rr, qq, aa, kk, cc, ok2 := Parse([]byte(FormatKeepAlive(keepalive.OpenSIPSIP, keepalive.Status)))
			if !ok2 || kk == nil || rr != nil || qq != nil || aa != nil || cc != nil {
				t.Fatalf("roundtrip keepalive failed: ok=%v keepalive=%v", ok2, kk)
			}
		}
		if conv != nil {
			rr, qq, aa, kk, cc, ok2 := Parse([]byte(FormatClientConversation(conv.SipID)))
			if !ok2 || cc == nil || rr != nil || qq != nil || aa != nil || kk != nil {
				t.Fatalf("roundtrip conv failed: ok=%v conv=%v", ok2, cc)
			}
		}
	})
}

package protocol

import "testing"

func FuzzParse(f *testing.F) {
	qSeed, err := FormatClientQuery("AA:BB:CC:DD:EE:FF")
	if err != nil {
		panic(err)
	}
	seeds := [][]byte{
		[]byte(""),
		[]byte("   "),
		[]byte("unknown_cmd"),
		[]byte(FormatClientReset("1.2.3.4", "5.6.7.8")),
		[]byte(qSeed),
		[]byte("ec_client_query AA:BB CC"),
		[]byte(FormatClientAnswer("1.2.3.4", "123")),
		[]byte(FormatKeepAlive("1.2.3.4", "0")),
		[]byte(FormatClientConversation("123")),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		pkt, ok := Parse(b)
		if !ok {
			return
		}
		if pkt.Type == ECPacketUnknown {
			t.Fatalf("ok=true but Type is unknown")
		}
		if pkt.Reset == nil && pkt.Query == nil && pkt.Answer == nil && pkt.KeepAlive == nil && pkt.Conversation == nil {
			t.Fatalf("ok=true but all payloads are nil (pkt=%+v)", pkt)
		}

		if pkt.Reset != nil {
			p2, ok2 := Parse([]byte(FormatClientReset(pkt.Reset.IPHead1, pkt.Reset.IPHead2)))
			if !ok2 || p2.Type != ECPacketClientReset || p2.Reset == nil || p2.Query != nil || p2.Answer != nil || p2.KeepAlive != nil || p2.Conversation != nil {
				t.Fatalf("roundtrip reset failed: ok=%v pkt=%+v", ok2, p2)
			}
		}
		if pkt.Query != nil {
			formatted, ferr := FormatClientQuery(pkt.Query.MAC)
			if ferr != nil {
				t.Fatalf("FormatClientQuery after Parse ok: %v mac=%q", ferr, pkt.Query.MAC)
			}
			p2, ok2 := Parse([]byte(formatted))
			if !ok2 || p2.Type != ECPacketClientQuery || p2.Query == nil || p2.Reset != nil || p2.Answer != nil || p2.KeepAlive != nil || p2.Conversation != nil {
				t.Fatalf("roundtrip query failed: ok=%v pkt=%+v", ok2, p2)
			}
		}
		if pkt.Answer != nil {
			p2, ok2 := Parse([]byte(FormatClientAnswer(pkt.Answer.NewIP, pkt.Answer.SipID)))
			if !ok2 || p2.Type != ECPacketClientAnswer || p2.Answer == nil || p2.Reset != nil || p2.Query != nil || p2.KeepAlive != nil || p2.Conversation != nil {
				t.Fatalf("roundtrip answer failed: ok=%v pkt=%+v", ok2, p2)
			}
		}
		if pkt.KeepAlive != nil {
			p2, ok2 := Parse([]byte(FormatKeepAlive(pkt.KeepAlive.OpenSIPSIP, pkt.KeepAlive.Status)))
			if !ok2 || p2.Type != ECPacketServerKeepAlive || p2.KeepAlive == nil || p2.Reset != nil || p2.Query != nil || p2.Answer != nil || p2.Conversation != nil {
				t.Fatalf("roundtrip keepalive failed: ok=%v pkt=%+v", ok2, p2)
			}
		}
		if pkt.Conversation != nil {
			p2, ok2 := Parse([]byte(FormatClientConversation(pkt.Conversation.SipID)))
			if !ok2 || p2.Type != ECPacketClientConversation || p2.Conversation == nil || p2.Reset != nil || p2.Query != nil || p2.Answer != nil || p2.KeepAlive != nil {
				t.Fatalf("roundtrip conv failed: ok=%v pkt=%+v", ok2, p2)
			}
		}
	})
}

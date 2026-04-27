package rtp

import (
	"testing"

	pionrtp "github.com/pion/rtp"
)

func TestNewPacket_MarshalUnmarshal(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	pkt := NewPacket(0x1122, 0x33445566, 0xdeadbeef, payload)
	raw, err := pkt.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got pionrtp.Packet
	if err := got.Unmarshal(raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PayloadType != PayloadTypeG726() {
		t.Fatalf("PayloadType=%d want %d", got.PayloadType, PayloadTypeG726())
	}
	if got.SequenceNumber != 0x1122 || got.Timestamp != 0x33445566 || got.SSRC != 0xdeadbeef {
		t.Fatalf("header mismatch: seq=%d ts=%d ssrc=%d", got.SequenceNumber, got.Timestamp, got.SSRC)
	}
	if string(got.Payload) != string(payload) {
		t.Fatalf("payload=%v want %v", got.Payload, payload)
	}
}

package rtp

import (
	"os"
	"sync"
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

func resetPayloadTypeForTest(t *testing.T) {
	t.Helper()
	payloadTypeOnce = sync.Once{}
	payloadTypeG726Val = DefaultPayloadTypeG726
	_ = os.Unsetenv(envPayloadTypeG726Key)
}

func TestPayloadTypeG726_Default(t *testing.T) {
	resetPayloadTypeForTest(t)
	if got := PayloadTypeG726(); got != DefaultPayloadTypeG726 {
		t.Fatalf("PayloadTypeG726()=%d want %d", got, DefaultPayloadTypeG726)
	}
}

func TestPayloadTypeG726_Env_ValidDynamic(t *testing.T) {
	resetPayloadTypeForTest(t)
	t.Setenv(envPayloadTypeG726Key, "96")
	if got := PayloadTypeG726(); got != 96 {
		t.Fatalf("PayloadTypeG726()=%d want %d", got, 96)
	}
}

func TestPayloadTypeG726_Env_ValidStatic2(t *testing.T) {
	resetPayloadTypeForTest(t)
	t.Setenv(envPayloadTypeG726Key, "2")
	if got := PayloadTypeG726(); got != 2 {
		t.Fatalf("PayloadTypeG726()=%d want %d", got, 2)
	}
}

func TestPayloadTypeG726_Env_Invalid_Ignored(t *testing.T) {
	cases := []string{"", "abc", "95", "128", "-1", "3"}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			resetPayloadTypeForTest(t)
			if tc != "" {
				t.Setenv(envPayloadTypeG726Key, tc)
			} else {
				_ = os.Unsetenv(envPayloadTypeG726Key)
			}
			if got := PayloadTypeG726(); got != DefaultPayloadTypeG726 {
				t.Fatalf("PayloadTypeG726()=%d want %d (tc=%q)", got, DefaultPayloadTypeG726, tc)
			}
		})
	}
}

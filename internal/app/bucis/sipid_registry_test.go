package bucis

import (
	"math"
	"strconv"
	"sync"
	"testing"
)

func TestSipIDState_LargeBootValue(t *testing.T) {
	s := &sipIDState{
		boot: uint64(math.MaxUint32),
		mod:  0,
		m:    make(map[string]string),
		ip:   make(map[string]string),
		ipr:  make(map[string]string),
	}

	id := s.GetOrCreate("AA:BB:CC:DD:EE:FF")
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		t.Fatalf("expected sipID to parse as uint64, got id=%q err=%v", id, err)
	}
	want := uint64(math.MaxUint32)*1_000_000 + 1
	if n != want {
		t.Fatalf("unexpected sipID number: got %d want %d (id=%q)", n, want, id)
	}
}

func TestSipIDState_Concurrent(t *testing.T) {
	s := newSipIDState(0)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mac := strconv.FormatInt(int64(i%256), 16)
			if len(mac) == 1 {
				mac = "0" + mac
			}
			mac = "AA:BB:CC:DD:EE:" + mac

			id := s.GetOrCreate(mac)
			s.SetIP(id, "10.0.0.1")
		}(i)
	}
	wg.Wait()
}

func TestBUCIS_ConversationRoutingWithOverride(t *testing.T) {
	s := newSipIDState(0)

	mac := "AA:BB:CC:DD:EE:FF"
	senderIP := "10.0.0.50"
	overrideIP := "192.0.2.10"

	sipID := s.GetOrCreate(mac)

	// Симулируем поведение BUCIS на ClientQuery:
	// если включён override, то и ClientAnswer, и последующий ClientConversation должны идти на override.
	dst := overrideIP
	s.SetIP(sipID, dst)

	if got, ok := s.GetIP(sipID); !ok || got != overrideIP {
		t.Fatalf("GetIP(%q) = %q,%v; want %q,true", sipID, got, ok, overrideIP)
	}
	if _, ok := s.FindBySenderIP(senderIP); ok {
		t.Fatalf("did not expect sipID mapping for senderIP=%q when override is enabled", senderIP)
	}
	if got, ok := s.FindBySenderIP(overrideIP); !ok || got != sipID {
		t.Fatalf("FindBySenderIP(%q) = %q,%v; want %q,true", overrideIP, got, ok, sipID)
	}
}

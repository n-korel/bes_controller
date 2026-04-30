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
	want := (uint64(math.MaxUint32) << 32) | 1
	if n != want {
		t.Fatalf("unexpected sipID number: got %d want %d (id=%q)", n, want, id)
	}
}

func TestSipIDState_NewBootIsUint32(t *testing.T) {
	s := newSipIDState(0)
	if s.boot > uint64(math.MaxUint32) {
		t.Fatalf("boot is out of uint32 range: %d", s.boot)
	}
}

func TestSipIDState_BootOverflow_NoCollision(t *testing.T) {
	// Регрессия: при формуле вида `boot*1_000_000 + seq` возможна коллизия, если seq >= 1_000_000:
	// (boot=1, seq=1_000_001) == (boot=2, seq=1).
	s1 := &sipIDState{
		boot: 1,
		seq:  1_000_000,
		mod:  0,
		m:    make(map[string]string),
		ip:   make(map[string]string),
		ipr:  make(map[string]string),
	}
	s2 := &sipIDState{
		boot: 2,
		seq:  0,
		mod:  0,
		m:    make(map[string]string),
		ip:   make(map[string]string),
		ipr:  make(map[string]string),
	}

	id1 := s1.GetOrCreate("AA:BB:CC:DD:EE:01") // seq -> 1_000_001
	id2 := s2.GetOrCreate("AA:BB:CC:DD:EE:02") // seq -> 1
	if id1 == id2 {
		t.Fatalf("expected no collision across boot boundary: id1=%q id2=%q", id1, id2)
	}
}

func TestSipIDState_BootUnixNanoFallback_Overflow(t *testing.T) {
	// UnixNano заведомо больше uint32; важно, чтобы fallback не ломался на переполнении и был детерминирован.
	unixNano := int64(math.MaxInt64)

	got := bootUnixNanoFallback(unixNano)
	u := uint64(unixNano)
	want := uint32(u ^ (u >> 32))

	if got != want {
		t.Fatalf("bootUnixNanoFallback(%d) = %d; want %d", unixNano, got, want)
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

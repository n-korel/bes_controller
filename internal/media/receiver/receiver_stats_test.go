package receiver

import (
	"testing"
	"time"
)

func TestSessionStats_ExpectedLostJitterMs(t *testing.T) {
	s := SessionStats{
		FirstSeq: 10,
		MaxSeq:   19,
		Cycles:   0,
		Received: 8,
		Jitter:   16,
	}
	if got, want := s.Expected(), uint32(10); got != want {
		t.Fatalf("Expected=%d want %d", got, want)
	}
	if got, want := s.Lost(), uint32(2); got != want {
		t.Fatalf("Lost=%d want %d", got, want)
	}
	if got, want := s.JitterMs(), 2.0; got != want {
		t.Fatalf("JitterMs=%v want %v", got, want)
	}
}

func TestIsNewerTimestamp_WrapBehavior(t *testing.T) {
	if !isNewerTimestamp(1, 0) {
		t.Fatalf("1 should be newer than 0")
	}
	// wrap: 0 считается "новее" maxUint32
	if !isNewerTimestamp(0, ^uint32(0)) {
		t.Fatalf("0 should be newer than max uint32 in wrap logic")
	}
	if isNewerTimestamp(^uint32(0), 0) {
		t.Fatalf("max uint32 should not be newer than 0")
	}
}

func TestReceiver_updateStats_WrapAroundCycles(t *testing.T) {
	r := New(0)
	r.playing = true

	// Первый пакет инициализирует статистику.
	r.updateStats(65530, 1000)
	s1 := r.StatsSnapshot()
	if s1.Cycles != 0 || s1.MaxSeq != 65530 {
		t.Fatalf("init: Cycles=%d MaxSeq=%d", s1.Cycles, s1.MaxSeq)
	}

	// Следующий seq после wrap (маленький), но timestamp явно "новее" → должна сработать ветка u1 и Cycles увеличится.
	r.updateStats(5, 2000)
	s2 := r.StatsSnapshot()
	if got, want := s2.Cycles, uint32(1<<16); got != want {
		t.Fatalf("Cycles=%d want %d", got, want)
	}
	if got, want := s2.MaxSeq, uint16(5); got != want {
		t.Fatalf("MaxSeq=%d want %d", got, want)
	}
	if s2.Received < 2 {
		t.Fatalf("Received=%d want >=2", s2.Received)
	}
}

func TestReceiver_StartBySoundType99_DeterministicNoNetwork(t *testing.T) {
	r := New(0)
	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("StartBySoundType(99): %v", err)
	}
	defer func() { _, _ = r.Stop() }()

	time.Sleep(60 * time.Millisecond)
	s := r.StatsSnapshot()
	if s.Received == 0 {
		t.Fatalf("Received=0; want >0")
	}
	if s.LastPacket.IsZero() {
		t.Fatalf("LastPacket is zero")
	}
}

func TestReceiver_StartStop_IdempotentScenarios(t *testing.T) {
	r := New(0)

	// Stop без Start: не должен паниковать/виснуть.
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop without start err=%v", err)
	}

	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("StartBySoundType(99): %v", err)
	}
	// Start два раза: по коду должен вернуть nil.
	if err := r.StartBySoundType(99); err != nil {
		t.Fatalf("Start second time: %v", err)
	}

	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Повторный Stop: тоже ок.
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop second time: %v", err)
	}
}


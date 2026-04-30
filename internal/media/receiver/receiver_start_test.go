package receiver

import "testing"

func TestReceiver_Start_WrapsStartBySoundType(t *testing.T) {
	r := New(0)
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

package bucis

import (
	"strings"
	"testing"
)

func FuzzClientQueryMACNormalization(f *testing.F) {
	seeds := []string{
		"AA:BB:CC:DD:EE:FF",
		"aa:bb:cc:dd:ee:ff",
		" Aa:Bb:Cc:Dd:Ee:Ff ",
		"00:11:22:33:44:55",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, mac string) {
		mac = strings.TrimSpace(mac)
		if mac == "" {
			return
		}
		if len(mac) > 128 {
			return
		}

		s := newSipIDState()

		gotUpper := s.GetOrCreate(strings.ToUpper(mac))
		gotLower := s.GetOrCreate(strings.ToLower(mac))
		if gotUpper != gotLower {
			t.Fatalf("sipID must be stable across MAC case: upper=%q lower=%q mac=%q", gotUpper, gotLower, mac)
		}

		gotSpaced := s.GetOrCreate("  " + mac + "  ")
		if gotUpper != gotSpaced {
			t.Fatalf("sipID must be stable across MAC whitespace: upper=%q spaced=%q mac=%q", gotUpper, gotSpaced, mac)
		}
	})
}


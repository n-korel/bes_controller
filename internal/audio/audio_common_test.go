package audio

import "testing"

func TestEnabled(t *testing.T) {
	t.Run("default_enabled", func(t *testing.T) {
		t.Setenv("NO_AUDIO", "")
		t.Setenv("ALSA_DEVICE", "")
		if !Enabled() {
			t.Fatal("expected Enabled()=true by default")
		}
	})

	t.Run("no_audio_disables", func(t *testing.T) {
		t.Setenv("NO_AUDIO", "1")
		t.Setenv("ALSA_DEVICE", "default")
		if Enabled() {
			t.Fatal("expected Enabled()=false when NO_AUDIO=1")
		}
	})

	t.Run("null_device_disables", func(t *testing.T) {
		t.Setenv("NO_AUDIO", "")
		t.Setenv("ALSA_DEVICE", " null ")
		if Enabled() {
			t.Fatal("expected Enabled()=false when ALSA_DEVICE=null")
		}
	})
}

func TestDevice(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("ALSA_DEVICE", "")
		if got := Device(); got != "default" {
			t.Fatalf("Device()=%q want %q", got, "default")
		}
	})

	t.Run("trim_space", func(t *testing.T) {
		t.Setenv("ALSA_DEVICE", " hw:0,0 ")
		if got := Device(); got != "hw:0,0" {
			t.Fatalf("Device()=%q want %q", got, "hw:0,0")
		}
	})
}

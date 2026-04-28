package audio

import (
	"os"
	"testing"
)

func TestEnabled_NO_AUDIO_Disables(t *testing.T) {
	t.Setenv(envNoAudioKey, "1")
	t.Setenv(envALSADeviceKey, defaultALSACard)
	if Enabled() {
		t.Fatalf("Enabled()=true want false")
	}
}

func TestEnabled_ALSA_DEVICE_Null_Disables(t *testing.T) {
	t.Setenv(envNoAudioKey, "")
	t.Setenv(envALSADeviceKey, "null")
	if Enabled() {
		t.Fatalf("Enabled()=true want false")
	}
}

func TestEnabled_DefaultDevice_Enables(t *testing.T) {
	t.Setenv(envNoAudioKey, "")
	t.Setenv(envALSADeviceKey, "")
	if !Enabled() {
		t.Fatalf("Enabled()=false want true")
	}
}

func TestEnabled_WhitespaceDevice_Defaults(t *testing.T) {
	t.Setenv(envNoAudioKey, "")
	t.Setenv(envALSADeviceKey, "   ")
	if !Enabled() {
		t.Fatalf("Enabled()=false want true")
	}
}

func TestDevice_DefaultAndTrim(t *testing.T) {
	t.Setenv(envALSADeviceKey, "")
	if got := Device(); got != defaultALSACard {
		t.Fatalf("Device()=%q want %q", got, defaultALSACard)
	}

	t.Setenv(envALSADeviceKey, "  hw:1,0  ")
	if got := Device(); got != "hw:1,0" {
		t.Fatalf("Device()=%q want %q", got, "hw:1,0")
	}
}

func TestEnabled_DoesNotDependOnRealEnv(t *testing.T) {
	// Sanity check: ensure tests do not leak env changes across cases.
	_ = os.Unsetenv(envNoAudioKey)
	_ = os.Unsetenv(envALSADeviceKey)
	// The exact result may depend on host env; assert only that call does not panic.
	_ = Enabled()
}

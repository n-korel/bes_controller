package audio

import (
	"os"
	"strings"
	"time"
)

const (
	SampleRate      = 8000
	Channels        = 1
	SamplesPerFrame = 160
	FrameDuration   = 20 * time.Millisecond

	bytesPerSample   = 2
	frameBytes       = SamplesPerFrame * bytesPerSample
	defaultALSACard  = "default"
	envALSADeviceKey = "ALSA_DEVICE"
	envNoAudioKey    = "NO_AUDIO"
)

func Enabled() bool {
	if strings.TrimSpace(os.Getenv(envNoAudioKey)) == "1" {
		return false
	}
	dev := strings.TrimSpace(os.Getenv(envALSADeviceKey))
	if dev == "" {
		dev = defaultALSACard
	}
	return dev != "null"
}

// Device returns the configured audio device identifier.
//
// On Linux this maps to ALSA device name (used by arecord/aplay).
// On Windows this maps to the capture device name (used by ffmpeg -f dshow).
func Device() string {
	dev := strings.TrimSpace(os.Getenv(envALSADeviceKey))
	if dev == "" {
		return defaultALSACard
	}
	return dev
}

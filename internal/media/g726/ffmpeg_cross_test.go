package g726

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	ffmpegSampleRate     = 8000
	ffmpegChannels       = 1
	ffmpegBitrate        = "32k"
	samplesPerFrameG726  = 160
	bytesPerFrameG726_32 = 80
)

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
	return path
}

func writeS16LE(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	buf.Grow(len(samples) * 2)
	for _, s := range samples {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(s))
		buf.Write(b[:])
	}
	_, err = f.Write(buf.Bytes())
	return err
}

func readS16LE(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b)%2 != 0 {
		return nil, fmt.Errorf("s16le file has odd size: %d", len(b))
	}
	out := make([]int16, len(b)/2)
	for i := 0; i < len(out); i++ {
		u := binary.LittleEndian.Uint16(b[i*2 : i*2+2])
		out[i] = int16(u)
	}
	return out, nil
}

func runFFmpeg(ffmpegPath string, args ...string) ([]byte, error) {
	args = append([]string{"-y"}, args...)
	cmd := exec.Command(ffmpegPath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), fmt.Errorf("ffmpeg failed: %w\nargs=%v\nout:\n%s", err, args, out.String())
	}
	return out.Bytes(), nil
}

type pcmMetrics struct {
	rms    float64
	maxAbs int
}

func calcMetrics(ref, got []int16, start int) pcmMetrics {
	if start < 0 {
		start = 0
	}
	if start > len(ref) {
		start = len(ref)
	}
	if start > len(got) {
		start = len(got)
	}
	n := 0
	var sumSq float64
	maxAbs := 0
	for i := start; i < len(ref) && i < len(got); i++ {
		d := int(ref[i]) - int(got[i])
		if d < 0 {
			d = -d
		}
		if d > maxAbs {
			maxAbs = d
		}
		sumSq += float64(d * d)
		n++
	}
	rms := 0.0
	if n > 0 {
		rms = math.Sqrt(sumSq / float64(n))
	}
	return pcmMetrics{rms: rms, maxAbs: maxAbs}
}

func noiseSamples(n int, seed int64, amp int16) []int16 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int16, n)
	for i := range out {
		v := r.Intn(int(amp)*2+1) - int(amp)
		out[i] = int16(v)
	}
	return out
}

func makeTestPCM() []int16 {
	const frames = 80
	const n = frames * samplesPerFrameG726

	var out []int16
	out = append(out, sineSamples(n, 1000, ffmpegSampleRate)...)

	out2 := noiseSamples(n, 1, 2500)
	out = append(out, out2...)

	out3 := make([]int16, n)
	for i := 0; i < len(out3); i += 400 {
		out3[i] = 12000
	}
	out = append(out, out3...)

	return out
}

func encodeWithFFmpegToRawG726(t *testing.T, ffmpegPath string, pcm []int16, codec string, outG726Path string) {
	t.Helper()
	dir := filepath.Dir(outG726Path)
	inPCM := filepath.Join(dir, "in.pcm")
	if err := writeS16LE(inPCM, pcm); err != nil {
		t.Fatalf("write pcm: %v", err)
	}

	// Container format depends on the bit-packing variant:
	// - g726   -> -f g726   (big-endian / "left-justified")
	// - g726le -> -f g726le (little-endian / "right-justified")
	format := "g726"
	if codec == "g726le" {
		format = "g726le"
	}

	_, err := runFFmpeg(ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-f", "s16le", "-ar", fmt.Sprint(ffmpegSampleRate), "-ac", fmt.Sprint(ffmpegChannels), "-i", inPCM,
		"-c:a", codec, "-b:a", ffmpegBitrate,
		"-f", format, outG726Path,
	)
	if err != nil {
		t.Fatalf("ffmpeg encode (%s): %v", codec, err)
	}
}

func decodeWithFFmpegRawG726ToPCM(t *testing.T, ffmpegPath string, inG726Path string, format string, outPCMPath string) {
	t.Helper()
	_, err := runFFmpeg(ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-f", format, "-ar", fmt.Sprint(ffmpegSampleRate), "-ac", fmt.Sprint(ffmpegChannels), "-i", inG726Path,
		"-f", "s16le", "-ar", fmt.Sprint(ffmpegSampleRate), "-ac", fmt.Sprint(ffmpegChannels),
		outPCMPath,
	)
	if err != nil {
		t.Fatalf("ffmpeg decode: %v", err)
	}
}

func decodeWithOursRawG726ToPCM(t *testing.T, g726Raw []byte) []int16 {
	t.Helper()
	var dec G726DecoderState
	got := make([]int16, 0, len(g726Raw)*2)
	for i := 0; i < len(g726Raw); i += bytesPerFrameG726_32 {
		end := i + bytesPerFrameG726_32
		if end > len(g726Raw) {
			break
		}
		got = append(got, G726DecodeFrame(g726Raw[i:end], &dec)...)
	}
	return got
}

func encodeWithOursPCMToRawG726(pcm []int16) []byte {
	var enc G726EncoderState
	out := make([]byte, 0, len(pcm)/2)
	for i := 0; i < len(pcm); i += samplesPerFrameG726 {
		end := i + samplesPerFrameG726
		if end > len(pcm) {
			break
		}
		out = append(out, G726EncodeFrame(pcm[i:end], &enc)...)
	}
	return out
}

func TestG726CrossCheck_FFmpegEncode_OursDecode(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)

	pcm := makeTestPCM()
	const warmupFrames = 6
	const warmup = warmupFrames * samplesPerFrameG726

	tmp := t.TempDir()
	g726Path := filepath.Join(tmp, "out.g726")

	// Our "low nibble first" packing matches ffmpeg raw g726le on most builds.
	// Keep g726 as a fallback since ffmpeg packaging can vary across builds.
	candidates := []string{"g726le", "g726"}

	var errs []error
	for _, codec := range candidates {
		encodeWithFFmpegToRawG726(t, ffmpegPath, pcm, codec, g726Path)
		raw, err := os.ReadFile(g726Path)
		if err != nil {
			t.Fatalf("read g726: %v", err)
		}
		got := decodeWithOursRawG726ToPCM(t, raw)
		if len(got) < len(pcm) {
			errs = append(errs, fmt.Errorf("codec=%s: decoded pcm too short: got %d want >= %d", codec, len(got), len(pcm)))
			continue
		}
		got = got[:len(pcm)]

		m := calcMetrics(pcm, got, warmup)
		// RMS is the primary mismatch signal; maxAbs can spike on impulses.
		if m.rms <= 700 && m.maxAbs <= 20000 {
			return
		}
		errs = append(errs, fmt.Errorf("codec=%s: metrics too high: maxAbs=%d rms=%.2f", codec, m.maxAbs, m.rms))
	}
	for _, err := range errs {
		t.Log(err)
	}
	t.Fatalf("no ffmpeg codec variant matched our decoding (see logs above)")
}

func TestG726CrossCheck_OursEncode_FFmpegDecode(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)

	pcm := makeTestPCM()
	const warmupFrames = 6
	const warmup = warmupFrames * samplesPerFrameG726

	tmp := t.TempDir()
	inG726Path := filepath.Join(tmp, "in.g726")
	outPCMPath := filepath.Join(tmp, "out.pcm")

	raw := encodeWithOursPCMToRawG726(pcm)
	if err := os.WriteFile(inG726Path, raw, 0o644); err != nil {
		t.Fatalf("write g726: %v", err)
	}

	// If format/packing are incompatible, ffmpeg may fail or produce very bad PCM — metrics should catch it.
	// Our packing corresponds to raw little-endian g726le.
	decodeWithFFmpegRawG726ToPCM(t, ffmpegPath, inG726Path, "g726le", outPCMPath)
	got, err := readS16LE(outPCMPath)
	if err != nil {
		t.Fatalf("read pcm: %v", err)
	}
	if len(got) < len(pcm) {
		t.Fatalf("decoded pcm too short: got %d want >= %d", len(got), len(pcm))
	}
	got = got[:len(pcm)]

	m := calcMetrics(pcm, got, warmup)
	// RMS is the primary mismatch signal; keep maxAbs lenient due to impulses.
	if m.rms > 700 || m.maxAbs > 20000 {
		t.Fatalf("metrics too high: maxAbs=%d rms=%.2f", m.maxAbs, m.rms)
	}
}

func TestG726CrossCheck_SmokeFFmpegG726Demuxer(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)

	tmp := t.TempDir()
	inPCM := filepath.Join(tmp, "in.pcm")
	outG726 := filepath.Join(tmp, "out.g726")
	outPCM := filepath.Join(tmp, "out.pcm")

	pcm := sineSamples(samplesPerFrameG726*10, 1000, ffmpegSampleRate)
	if err := writeS16LE(inPCM, pcm); err != nil {
		t.Fatalf("write pcm: %v", err)
	}

	_, err := runFFmpeg(ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-f", "s16le", "-ar", fmt.Sprint(ffmpegSampleRate), "-ac", "1", "-i", inPCM,
		"-c:a", "g726le", "-b:a", ffmpegBitrate,
		"-f", "g726le", outG726,
	)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("ffmpeg missing g726le support or failed: %v", err)
		}
		t.Fatalf("ffmpeg encode smoke: %v", err)
	}

	decodeWithFFmpegRawG726ToPCM(t, ffmpegPath, outG726, "g726le", outPCM)
}


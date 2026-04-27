package g726

import (
	"fmt"
	"math"
	"testing"
)

func float11Eq(a, b float11) bool {
	return a.sign == b.sign && a.exp == b.exp && a.mant == b.mant
}

func coresEqual(enc *G726EncoderState, dec *G726DecoderState) bool {
	a, b := &enc.st, &dec.st
	if a.code != b.code {
		return false
	}
	for i := range a.sr {
		if !float11Eq(a.sr[i], b.sr[i]) {
			return false
		}
	}
	for i := range a.dq {
		if !float11Eq(a.dq[i], b.dq[i]) {
			return false
		}
	}
	for i := range a.a {
		if a.a[i] != b.a[i] {
			return false
		}
	}
	for i := range a.b {
		if a.b[i] != b.b[i] {
			return false
		}
	}
	for i := range a.pk {
		if a.pk[i] != b.pk[i] {
			return false
		}
	}
	return a.ap == b.ap && a.yu == b.yu && a.yl == b.yl &&
		a.dms == b.dms && a.dml == b.dml && a.td == b.td &&
		a.se == b.se && a.sez == b.sez && a.y == b.y
}

func TestG726EncodeFrame160Bytes(t *testing.T) {
	samples := make([]int16, 160)
	var enc G726EncoderState
	payload := G726EncodeFrame(samples, &enc)
	if len(payload) != 80 {
		t.Fatalf("len(payload)=%d, want 80", len(payload))
	}
}

func TestG726EncoderDecoderStateMatchesAfterEachSample(t *testing.T) {
	var enc G726EncoderState
	var dec G726DecoderState
	n := 500
	for i := 0; i < n; i++ {
		x := int16((i*173 + i*i) % 12000)
		if i%2 == 0 {
			x = -x
		}
		code := EncodeLinear(x, &enc)
		DecodeLinear(code, &dec)
		if !coresEqual(&enc, &dec) {
			t.Fatalf("core mismatch after sample %d", i)
		}
	}
}

func TestG726RoundTripSilence(t *testing.T) {
	const n = 160 * 10
	samples := make([]int16, n)
	var enc G726EncoderState
	var dec G726DecoderState
	payload := G726EncodeFrame(samples, &enc)
	got := G726DecodeFrame(payload, &dec)
	if len(got) != n {
		t.Fatalf("len(got)=%d want %d", len(got), n)
	}
	maxErr := maxAbsDiff(samples, got)
	if maxErr > 8 {
		t.Fatalf("silence max abs err %d, want <= 8", maxErr)
	}
}

func sineSamples(n int, freqHz float64, sr float64) []int16 {
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		v := math.Sin(2 * math.Pi * freqHz * float64(i) / sr)
		out[i] = int16(v * 3000)
	}
	return out
}

func maxAbsDiff(a, b []int16) int {
	var m int
	for i := range a {
		d := int(a[i] - b[i])
		if d < 0 {
			d = -d
		}
		if d > m {
			m = d
		}
	}
	return m
}

func maxAbsDiffFrom(a, b []int16, start int) int {
	if start < 0 {
		start = 0
	}
	if start > len(a) {
		start = len(a)
	}
	if start > len(b) {
		start = len(b)
	}
	var m int
	for i := start; i < len(a) && i < len(b); i++ {
		d := int(a[i] - b[i])
		if d < 0 {
			d = -d
		}
		if d > m {
			m = d
		}
	}
	return m
}

func TestG726RoundTripSines(t *testing.T) {
	const n = 160 * 50
	const sr = 8000.0
	const warmupFrames = 6
	const warmup = warmupFrames * 160
	for _, hz := range []float64{300, 1000, 3200} {
		hz := hz
		t.Run(fmt.Sprintf("%gHz", hz), func(t *testing.T) {
			t.Parallel()
			in := sineSamples(n, hz, sr)
			var enc G726EncoderState
			var dec G726DecoderState
			out := G726DecodeFrame(G726EncodeFrame(in, &enc), &dec)
			if len(out) != n {
				t.Fatalf("len(out)=%d want %d", len(out), n)
			}
			m := maxAbsDiffFrom(in, out, warmup)
			if m > 1000 {
				t.Fatalf("freq=%v Hz max abs err(after warmup=%d) %d, want <= 1000", hz, warmup, m)
			}
		})
	}
}

func TestG726OddSampleCountSecondNibbleZero(t *testing.T) {
	samples := []int16{100, 200, 300}
	var enc G726EncoderState
	payload := G726EncodeFrame(samples, &enc)
	if len(payload) != 2 {
		t.Fatalf("len(payload)=%d want 2", len(payload))
	}
	var dec G726DecoderState
	got := G726DecodeFrame(payload, &dec)
	if len(got) != 4 {
		t.Fatalf("len(got)=%d want 4", len(got))
	}
	var enc2 G726EncoderState
	var dec2 G726DecoderState
	full := []int16{100, 200, 300, 0}
	payloadFull := G726EncodeFrame(full, &enc2)
	gotFull := G726DecodeFrame(payloadFull, &dec2)
	for i := 0; i < 3; i++ {
		if got[i] != gotFull[i] {
			t.Fatalf("sample %d: odd=%d full=%d", i, got[i], gotFull[i])
		}
	}
}

func TestG726DeterministicInitialState(t *testing.T) {
	in := sineSamples(160*5, 1000, 8000)

	var enc1 G726EncoderState
	var enc2 G726EncoderState
	p1 := G726EncodeFrame(in, &enc1)
	p2 := G726EncodeFrame(in, &enc2)
	if len(p1) != len(p2) {
		t.Fatalf("len(p1)=%d len(p2)=%d", len(p1), len(p2))
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Fatalf("payload mismatch at byte %d: %d vs %d", i, p1[i], p2[i])
		}
	}

	var dec1 G726DecoderState
	var dec2 G726DecoderState
	out1 := G726DecodeFrame(p1, &dec1)
	out2 := G726DecodeFrame(p1, &dec2)
	if len(out1) != len(out2) {
		t.Fatalf("len(out1)=%d len(out2)=%d", len(out1), len(out2))
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("decode mismatch at sample %d: %d vs %d", i, out1[i], out2[i])
		}
	}
}

func TestClipIntAndClipIntp2Boundaries(t *testing.T) {
	if got := clipInt(10, -5, 5); got != 5 {
		t.Fatalf("clipInt(10,-5,5)=%d want 5", got)
	}
	if got := clipInt(-10, -5, 5); got != -5 {
		t.Fatalf("clipInt(-10,-5,5)=%d want -5", got)
	}
	if got := clipInt(3, -5, 5); got != 3 {
		t.Fatalf("clipInt(3,-5,5)=%d want 3", got)
	}

	const p = 8
	if got := clipIntp2(math.MaxInt32, p); got != (1<<p)-1 {
		t.Fatalf("clipIntp2(MaxInt32,%d)=%d want %d", p, got, (1<<p)-1)
	}
	if got := clipIntp2(math.MinInt32, p); got != -1<<p {
		t.Fatalf("clipIntp2(MinInt32,%d)=%d want %d", p, got, -1<<p)
	}
	if got := clipIntp2((1<<p)-1, p); got != (1<<p)-1 {
		t.Fatalf("clipIntp2(%d,%d)=%d want %d", (1<<p)-1, p, got, (1<<p)-1)
	}
	if got := clipIntp2(-1<<p, p); got != -1<<p {
		t.Fatalf("clipIntp2(%d,%d)=%d want %d", -1<<p, p, got, -1<<p)
	}
}

func TestG726EncodeDecodeHandlesInt16Extremes(t *testing.T) {
	in := []int16{
		math.MinInt16,
		math.MaxInt16,
		math.MinInt16 + 1,
		math.MaxInt16 - 1,
		-1, 0, 1,
	}
	in = append(in, make([]int16, 160-len(in))...)

	var enc G726EncoderState
	var dec G726DecoderState
	payload := G726EncodeFrame(in, &enc)
	out := G726DecodeFrame(payload, &dec)
	if len(out) != 160 {
		t.Fatalf("len(out)=%d want 160", len(out))
	}
}

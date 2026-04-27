package g726

import (
	"math"
	"math/bits"
)

const (
	// ITU-T G.726, Annex A: internal fixed-point conventions used by the algorithm blocks.
	//
	// LOG/ANTILOG domain uses 7 fractional bits (values masked by 0x7f) and (exp<<7)+mant packing.
	g726LogFracBits = 7
	g726LogFracMask = (1 << g726LogFracBits) - 1 // 0x7f

	// ITU-T G.726, Annex A, block FMULT: rounding constant before >>4 in mantissa multiply.
	g726FmultRounding = 0x30
	g726FmultMantShift = 4

	// ITU-T G.726, Annex A, block FMULT: exponent alignment constant for this float11 format.
	// Matches the reference algorithm’s exponent handling for (4-bit exp, 6-bit mantissa) multiply.
	g726FmultExpBias = 19

	// ITU-T G.726, Annex A, block LIMB: limits for non-steady state step size multiplier yu.
	g726YuMin = 544
	g726YuMax = 5120

	// ITU-T G.726, Annex A, blocks LIMC/LIMD: predictor coefficient limits (32 kbit/s mode).
	g726A2Limit = 12288
	g726A1LimitBase = 15360

	// ITU-T G.726, Annex A, block TONE: a2 threshold for tone/modem (data) detection.
	g726ToneA2Threshold = -11776
)

type float11 struct {
	sign uint8
	exp  uint8
	mant uint8
}

type g726Core struct {
	sr [2]float11
	dq [6]float11
	a  [2]int
	b  [6]int
	pk [2]int

	ap  int
	yu  int
	yl  int
	dms int
	dml int
	td  int

	se   int
	sez  int
	y    int
	code int

	initialized bool
}

type G726EncoderState struct {
	st g726Core
}

type G726DecoderState struct {
	st g726Core
}

func initCore(c *g726Core) {
	c.code = 4
	c.initialized = true
	for i := range c.sr {
		c.sr[i].mant = 1 << 5
		c.pk[i] = 1
	}
	for i := range c.dq {
		c.dq[i].mant = 1 << 5
	}
	c.yu = g726YuMin
	c.yl = 34816
	c.y = g726YuMin
}

func log2_16bitSaturating(v int) int {
	if v <= 0 {
		return 0
	}
	if v > 0xffff {
		v = 0xffff
	}
	return bits.Len16(uint16(v)) - 1
}

func i2f(i int, f *float11) {
	if i < 0 {
		f.sign = 1
		i = -i
	} else {
		f.sign = 0
	}
	if i != 0 {
		f.exp = uint8(log2_16bitSaturating(i) + 1)
		f.mant = uint8((i << 6) >> f.exp)
	} else {
		f.exp = 0
		f.mant = 1 << 5
	}
}

func mult(f1, f2 *float11) int {
	exp := int(f1.exp + f2.exp)
	res := (int(f1.mant)*int(f2.mant) + g726FmultRounding) >> g726FmultMantShift
	if exp > g726FmultExpBias {
		res <<= exp - g726FmultExpBias
	} else {
		res >>= g726FmultExpBias - exp
	}
	if (f1.sign ^ f2.sign) != 0 {
		return -res
	}
	return res
}

func sgn(value int) int {
	if value < 0 {
		return -1
	}
	return 1
}

func clipInt(a, amin, amax int) int {
	if a < amin {
		return amin
	}
	if a > amax {
		return amax
	}
	return a
}

func clipIntp2(a, p int) int {
	if ((uint32(a) + uint32(1<<p)) &^ uint32((2<<p)-1)) != 0 {
		return (a >> 31) ^ ((1 << p) - 1)
	}
	return a
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ITU-T G.726, Annex A (32 kbit/s): quantizer decision levels table for QUAN search.
// See the Annex A table for the 32 kbit/s quantizer decision thresholds (qtab_726_32).
var quantTbl32 = [...]int32{-125, 79, 177, 245, 299, 348, 399, math.MaxInt32}

// ITU-T G.726, Annex A (32 kbit/s): inverse quantizer table (dqlntab) for RECONST.
var iquantTbl32 = [...]int16{
	math.MinInt16, 4, 135, 213, 273, 323, 373, 425,
	425, 373, 323, 273, 213, 135, 4, math.MinInt16,
}

// ITU-T G.726, Annex A (32 kbit/s): scale factor multiplier table (witab) for adaptation.
var wTbl32 = [...]int16{
	-12, 18, 41, 64, 112, 198, 355, 1122,
	1122, 355, 198, 112, 64, 41, 18, -12,
}

// ITU-T G.726, Annex A (32 kbit/s): stationary/non-stationary indicator table (fitab).
var fTbl32 = [...]uint8{
	0, 0, 0, 1, 1, 1, 3, 7, 7, 3, 1, 1, 1, 0, 0, 0,
}

func quantAdapt(c *g726Core, d int) int {
	sign := 0
	i := 0
	if d < 0 {
		sign = 1
		d = -d
	}
	exp := log2_16bitSaturating(d)
	dln := ((exp << g726LogFracBits) + (((d << g726LogFracBits) >> exp) & g726LogFracMask)) - (c.y >> 2)
	for quantTbl32[i] < math.MaxInt32 && int(quantTbl32[i]) < dln {
		i++
	}
	if sign != 0 {
		i = ^i
	}
	if i == 0 {
		i = 0xff
	}
	return i
}

func inverseQuant(c *g726Core, i int) int16 {
	dql := int(iquantTbl32[i]) + (c.y >> 2)
	dex := (dql >> g726LogFracBits) & 0xf
	dqt := (1 << g726LogFracBits) + (dql & g726LogFracMask)
	if dql < 0 {
		return 0
	}
	return int16((dqt << dex) >> g726LogFracBits)
}

func g726DecodeUpdate(c *g726Core, I int) int16 {
	I &= (1 << c.code) - 1
	I_sig := I >> (c.code - 1)

	dq := int(inverseQuant(c, I))

	ylint := c.yl >> 15
	ylfrac := (c.yl >> 10) & 0x1f
	var thr2 int
	if ylint > 9 {
		thr2 = 0x1f << 10
	} else {
		thr2 = (0x20 + ylfrac) << ylint
	}
	tr := c.td == 1 && dq > ((3*thr2)>>2)

	if I_sig != 0 {
		dq = -dq
	}
	reSignal := int16(c.se + dq)

	var pk0 int
	if c.sez+dq != 0 {
		pk0 = sgn(c.sez + dq)
	} else {
		pk0 = 0
	}
	dq0 := 0
	if dq != 0 {
		dq0 = sgn(dq)
	}

	if tr {
		c.a[0] = 0
		c.a[1] = 0
		for i := range c.b {
			c.b[i] = 0
		}
	} else {
		fa1 := clipIntp2((-c.a[0]*c.pk[0]*pk0)>>5, 8)
		c.a[1] += 128*pk0*c.pk[1] + fa1 - (c.a[1] >> 7)
		c.a[1] = clipInt(c.a[1], -g726A2Limit, g726A2Limit)
		c.a[0] += 64*3*pk0*c.pk[0] - (c.a[0] >> 8)
		c.a[0] = clipInt(c.a[0], -(g726A1LimitBase - c.a[1]), g726A1LimitBase-c.a[1])
		for i := range c.b {
			c.b[i] += 128*dq0*sgn(-int(c.dq[i].sign)) - (c.b[i] >> 8)
		}
	}

	c.pk[1] = c.pk[0]
	if pk0 != 0 {
		c.pk[0] = pk0
	} else {
		c.pk[0] = 1
	}
	c.sr[1] = c.sr[0]
	i2f(int(reSignal), &c.sr[0])
	for i := 5; i > 0; i-- {
		c.dq[i] = c.dq[i-1]
	}
	i2f(dq, &c.dq[0])
	c.dq[0].sign = uint8(I_sig)

	c.td = 0
	if c.a[1] < g726ToneA2Threshold {
		c.td = 1
	}

	c.dms += int(fTbl32[I])<<4 + ((-c.dms) >> 5)
	c.dml += int(fTbl32[I])<<4 + ((-c.dml) >> 7)
	if tr {
		c.ap = 256
	} else {
		c.ap += (-c.ap) >> 4
		if c.y <= 1535 || c.td != 0 || iabs((c.dms<<2)-c.dml) >= (c.dml>>3) {
			c.ap += 0x20
		}
	}

	c.yu = clipInt(c.y+int(wTbl32[I])+((-c.y)>>5), g726YuMin, g726YuMax)
	c.yl += c.yu + ((-c.yl) >> 6)

	al := c.ap >> 2
	if c.ap >= 256 {
		al = 1 << 6
	}
	c.y = (c.yl + (c.yu-(c.yl>>6))*al) >> 6

	c.se = 0
	for i := 0; i < 6; i++ {
		var tf float11
		i2f(c.b[i]>>2, &tf)
		c.se += mult(&tf, &c.dq[i])
	}
	c.sez = c.se >> 1
	for i := 0; i < 2; i++ {
		var tf float11
		i2f(c.a[i]>>2, &tf)
		c.se += mult(&tf, &c.sr[i])
	}
	c.se >>= 1

	out := int(reSignal) * 4
	if out > 0xffff {
		out = 0xffff
	}
	if out < -0xffff {
		out = -0xffff
	}
	return int16(out)
}

func EncodeLinear(sample int16, state *G726EncoderState) byte {
	if !state.st.initialized {
		initCore(&state.st)
	}
	d := int(sample)/4 - state.st.se
	qi := quantAdapt(&state.st, d)
	code := byte(qi) & 0x0f
	g726DecodeUpdate(&state.st, int(code))
	return code
}

func DecodeLinear(code byte, state *G726DecoderState) int16 {
	if !state.st.initialized {
		initCore(&state.st)
	}
	return g726DecodeUpdate(&state.st, int(code&0x0f))
}

func G726EncodeFrame(samples []int16, state *G726EncoderState) []byte {
	out := make([]byte, 0, (len(samples)+1)/2)
	for i := 0; i < len(samples); i += 2 {
		lo := EncodeLinear(samples[i], state)
		hi := byte(0)
		if i+1 < len(samples) {
			hi = EncodeLinear(samples[i+1], state)
		}
		out = append(out, lo|(hi<<4))
	}
	return out
}

func G726DecodeFrame(payload []byte, state *G726DecoderState) []int16 {
	out := make([]int16, 0, len(payload)*2)
	for _, b := range payload {
		lo := b & 0x0F
		hi := (b >> 4) & 0x0F
		out = append(out, DecodeLinear(lo, state))
		out = append(out, DecodeLinear(hi, state))
	}
	return out
}

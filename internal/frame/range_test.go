package frame

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/i32"
)

// rangeBlock is the block size of every frame built by this file's helpers.
const rangeBlock = 16

// writeEscapeResidual writes the residual section of a FIXED or LPC subframe in
// the Rice escape form (method 0, one partition, raw 31-bit values), which lets a
// fixture carry any residual the format allows, in range or not.
func writeEscapeResidual(bw *bitio.Writer, res []int64) {
	bw.WriteBits(0, 2)  // Rice, 4-bit parameters
	bw.WriteBits(0, 4)  // one partition
	bw.WriteBits(15, 4) // escape: raw residuals
	bw.WriteBits(31, 5) // raw width
	for _, v := range res {
		bw.WriteSignedBits(v, 31)
	}
}

// writeRawFixed writes a FIXED subframe of order len(warm) at width effBps, after
// shifting out `wasted` bits. res holds the rangeBlock-order residuals.
func writeRawFixed(bw *bitio.Writer, wasted, effBps int, warm, res []int64) {
	writeSubframeHeader(bw, 8+len(warm), wasted)
	for _, w := range warm {
		bw.WriteSignedBits(w, uint(effBps))
	}
	writeEscapeResidual(bw, res)
}

// writeRawLPC writes an LPC subframe of order len(warm) with precision-15
// coefficients (qlp precision field 14).
func writeRawLPC(bw *bitio.Writer, effBps int, warm []int64, shift int, coeffs []int32, res []int64) {
	writeSubframeHeader(bw, 31+len(warm), 0)
	for _, w := range warm {
		bw.WriteSignedBits(w, uint(effBps))
	}
	bw.WriteBits(14, 4) // precision 15
	bw.WriteSignedBits(int64(shift), 5)
	for _, c := range coeffs {
		bw.WriteSignedBits(int64(c), 15)
	}
	writeEscapeResidual(bw, res)
}

// rangeCase is one hand-built CRC-valid frame. Every sample either fits its
// bit depth (want is the expected output) or does not (reject).
type rangeCase struct {
	name   string
	bits   int // frame and STREAMINFO bit depth
	chCode int // frame header channel assignment code
	build  func(bw *bitio.Writer)
	reject bool
	want   [][]int32 // expected channels when !reject
}

// bytes assembles the case into frame bytes with a valid header CRC-8 and a
// valid frame CRC-16.
func (c rangeCase) bytes() []byte {
	bw := bitio.NewWriter()
	writeFrameHeader(bw, rangeBlock, c.chCode, 44100, c.bits, 0)
	c.build(bw)
	finishFrame(bw)
	return bw.Bytes()
}

func (c rangeCase) streamInfo() flac.StreamInfo {
	nch := 1
	if c.chCode >= chIndependent2 {
		nch = 2
	}
	return flac.StreamInfo{SampleRate: 44100, Channels: nch, BitDepth: c.bits}
}

// lpcNarrowCoeffs and the other fixture constants below are shared by the rows
// and by their precondition checks.
var (
	// lpcNarrowRowCoeffs: one coefficient, so the sum of absolute values is 16383.
	lpcNarrowRowCoeffs = []int32{16383}
	// lpcWideRowCoeffs: four coefficients summing to |A| = 2^16, the predicate edge.
	lpcWideRowCoeffs = []int32{-16384, -16384, -16384, -16384}
)

// rangeCases returns the frames of TestDecodeRejectsOutOfRangeSamples. FuzzDecode
// also seeds from it.
func rangeCases() []rangeCase {
	n := rangeBlock
	zeros := func(count int) []int64 { return make([]int64, count) }
	// Residuals that make an order-1 fixed predictor walk from `from` to `to` in
	// one step and then stay put.
	step1 := func(delta int64) []int64 {
		res := zeros(n - 1)
		res[0] = delta
		return res
	}
	return []rangeCase{
		{
			name: "lpc narrow route", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				res := zeros(n - 1)
				res[0] = 0x3fffffff
				writeRawLPC(bw, 16, []int64{0}, 15, lpcNarrowRowCoeffs, res)
			},
		},
		{
			name: "lpc int64 route", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawLPC(bw, 16, slices.Repeat([]int64{-32768}, 4), 1, lpcWideRowCoeffs, zeros(n-4))
			},
		},
		{
			name: "fixed order 0", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				res := zeros(n)
				res[3] = 40000
				writeRawFixed(bw, 0, 16, nil, res)
			},
		},
		{
			name: "fixed order 2", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				res := zeros(n - 2)
				res[0] = 100
				writeRawFixed(bw, 0, 16, []int64{32767, 32767}, res)
			},
		},
		{
			// 200 fits 16 bits but not the subframe's 8 effective bits; the shifted
			// value 51200 is also out of range, so only a check at the effective
			// width before the shift tells this from a check at the frame depth.
			name: "wasted bits beyond effective width", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 8, 8, []int64{0}, step1(200))
			},
		},
		{
			// 2^24 << 8 wraps to 0 in int32, so only a check at the subframe's
			// effective width (before the wasted-bits shift) sees it.
			name: "wasted bits wrap back in range", bits: 16, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 8, 8, []int64{0}, step1(1<<24))
			},
		},
		{
			name: "left/side right out of range", bits: 16, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{32767}, n), 0, 16)
				writeVerbatim(bw, slices.Repeat([]int32{-32768}, n), 0, 17)
			},
		},
		{
			name: "right/side left out of range", bits: 16, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{32767}, n), 0, 17)
				writeVerbatim(bw, slices.Repeat([]int32{32767}, n), 0, 16)
			},
		},
		{
			// mid 32767 and side 1 rebuild left = (65535+1)>>1 = 32768.
			name: "mid/side extreme", bits: 16, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{32767}, n), 0, 16)
				writeVerbatim(bw, slices.Repeat([]int32{1}, n), 0, 17)
			},
		},
		{
			// The side subframe leaves its own 17-bit range, so this is the
			// subframe check (not the output check) failing on a stereo channel.
			name: "side subframe beyond bps+1", bits: 16, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{0}, n), 0, 16)
				writeRawFixed(bw, 0, 17, []int64{0}, step1(70000))
			},
		},
		{
			name: "24-bit one past the maximum", bits: 24, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 24, []int64{1<<23 - 1}, step1(1))
			},
		},
		{
			name: "24-bit exact extremes", bits: 24, chCode: 0,
			build: func(bw *bitio.Writer) {
				res := zeros(n - 1)
				res[0] = 1            // 2^23-2 -> 2^23-1
				res[2] = -(1<<24 - 1) // 2^23-1 -> -2^23
				writeRawFixed(bw, 0, 24, []int64{1<<23 - 2}, res)
			},
			want: [][]int32{append([]int32{1<<23 - 2, 1<<23 - 1, 1<<23 - 1}, slices.Repeat([]int32{-(1 << 23)}, n-3)...)},
		},
		{
			// side = -65535 is the minimum of the 17-bit side range; a side check at
			// the frame depth (16 bits) would wrongly reject it.
			name: "left/side minimum side", bits: 16, chCode: chLeftSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{-32768}, n), 0, 16)
				writeVerbatim(bw, slices.Repeat([]int32{-65535}, n), 0, 17)
			},
			want: [][]int32{slices.Repeat([]int32{-32768}, n), slices.Repeat([]int32{32767}, n)},
		},
		{
			name: "28-bit independent", bits: 28, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 28, []int64{1<<27 - 1}, step1(1))
			},
		},
		{
			// 2^31 would wrap to -2^31 under int32(), which is in range.
			name: "32-bit independent narrowing wrap", bits: 32, chCode: 0, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 32, []int64{1<<31 - 1}, step1(1))
			},
		},
		{
			name: "32-bit left/side narrowing wrap", bits: 32, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeConstant64(bw, 1<<31-1, 0, 32)
				writeConstant64(bw, -1, 0, 33)
			},
		},
	}
}

// TestDecodeRejectsOutOfRangeSamples feeds CRC-valid frames whose samples leave
// their bit depth and expects flac.ErrSampleOutOfRange, a verdict libFLAC 1.5.0
// reaches too (its decoder compares the final output to the frame depth). The
// accept rows pin the boundaries so the limit cannot drift by one.
func TestDecodeRejectsOutOfRangeSamples(t *testing.T) {
	for _, c := range rangeCases() {
		t.Run(c.name, func(t *testing.T) {
			var fr Frame
			err := Decode(bitio.NewReader(bytes.NewReader(c.bytes())), c.streamInfo(), &fr)
			if c.reject {
				if !errors.Is(err, flac.ErrSampleOutOfRange) {
					t.Fatalf("Decode error = %v, want flac.ErrSampleOutOfRange", err)
				}
				if errors.Is(err, flac.ErrCRCMismatch) {
					t.Fatalf("Decode error %v also wraps ErrCRCMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(fr.Channels) != len(c.want) {
				t.Fatalf("got %d channels, want %d", len(fr.Channels), len(c.want))
			}
			for ch, want := range c.want {
				if !slices.Equal(fr.Channels[ch], want) {
					t.Fatalf("channel %d = %v, want %v", ch, fr.Channels[ch], want)
				}
			}
		})
	}
}

// TestDecodeRangeVerdictAfterCRC checks the reporting order: a frame whose
// samples are out of range but whose CRC-16 is broken reports ErrCRCMismatch,
// so a bit flip in a real stream keeps its corruption verdict.
func TestDecodeRangeVerdictAfterCRC(t *testing.T) {
	var c rangeCase
	for _, rc := range rangeCases() {
		if rc.name == "lpc narrow route" {
			c = rc
		}
	}
	if !c.reject {
		t.Fatal("fixture \"lpc narrow route\" missing or not an out-of-range frame")
	}
	raw := c.bytes()
	raw[len(raw)-1] ^= 0xff
	var fr Frame
	err := Decode(bitio.NewReader(bytes.NewReader(raw)), c.streamInfo(), &fr)
	if !errors.Is(err, flac.ErrCRCMismatch) {
		t.Fatalf("Decode error = %v, want ErrCRCMismatch", err)
	}
	if errors.Is(err, flac.ErrSampleOutOfRange) {
		t.Fatalf("Decode error %v reports the range verdict before the CRC", err)
	}
}

// TestRangeFixturesTakeTheirRoutes asserts the LPC fixtures sit on the restore
// route their names claim, so a change to the predicate cannot silently turn a
// narrow-route row into an int64-route one.
func TestRangeFixturesTakeTheirRoutes(t *testing.T) {
	if !i32.LPCRestoreNarrowSafe(lpcNarrowRowCoeffs, 16, 15) {
		t.Error("lpc narrow route fixture: predicate must hold")
	}
	if i32.LPCRestoreNarrowSafe(lpcWideRowCoeffs, 16, 1) {
		t.Error("lpc int64 route fixture: predicate must fail")
	}
}

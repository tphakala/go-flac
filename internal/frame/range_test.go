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
	// A block of zeros with one nonzero sample, in the widths the narrow (int32)
	// and wide (int64) paths take, and a constant int64 block.
	spike32 := func(idx int, v int32) []int32 {
		s := make([]int32, n)
		s[idx] = v
		return s
	}
	spike64 := func(idx int, v int64) []int64 {
		s := zeros(n)
		s[idx] = v
		return s
	}
	rep64 := func(v int64) []int64 { return slices.Repeat([]int64{v}, n) }
	// Expected output of an accept row whose channels are constant.
	wantConst := func(l, r int32) [][]int32 {
		return [][]int32{slices.Repeat([]int32{l}, n), slices.Repeat([]int32{r}, n)}
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
		{
			// mid -32768 and an odd side 3 rebuild right = -32769 while left stays
			// -32766, so only the right channel is out of range.
			name: "mid/side right-only overflow", bits: 16, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, slices.Repeat([]int32{-32768}, n), 0, 16)
				writeVerbatim(bw, slices.Repeat([]int32{3}, n), 0, 17)
			},
		},
		{
			// The first stereo subframe is out of range and a second one follows: the
			// decoder must still read the second to reach the CRC-16.
			name: "left/side first subframe out of range", bits: 16, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				res := zeros(n)
				res[3] = 40000
				writeRawFixed(bw, 0, 16, nil, res)
				writeVerbatim(bw, slices.Repeat([]int32{0}, n), 0, 17)
			},
		},
		{
			name: "independent stereo first subframe out of range", bits: 16, chCode: chIndependent2, reject: true,
			build: func(bw *bitio.Writer) {
				res := zeros(n)
				res[3] = 40000
				writeRawFixed(bw, 0, 16, nil, res)
				writeConstant(bw, 0, 0, 16)
			},
		},
		{
			// 28-bit frames take the int64 path. With m = 2*mid|side&1, mid 2^27-1 and
			// side 1 give left 2^27 and right 2^27-1: only the left channel leaves the
			// 28-bit range.
			name: "28-bit mid/side left-only overflow", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{1<<27 - 1}, n), 0, 28)
				writeVerbatim64(bw, slices.Repeat([]int64{1}, n), 0, 29)
			},
		},
		{
			// mid -2^27 and side 3 give left -2^27+2 and right -2^27-1: only the right
			// channel leaves the range.
			name: "28-bit mid/side right-only overflow", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{-(1 << 27)}, n), 0, 28)
				writeVerbatim64(bw, slices.Repeat([]int64{3}, n), 0, 29)
			},
		},
		{
			// mid 2^27-1 and side -1 give left 2^27-1 and right 2^27: only the right
			// channel leaves the range, above the maximum.
			name: "28-bit mid/side right positive overflow", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{1<<27 - 1}, n), 0, 28)
				writeVerbatim64(bw, slices.Repeat([]int64{-1}, n), 0, 29)
			},
		},
		{
			// mid -2^27 and side -3 give left -2^27-1 and right -2^27+2: only the left
			// channel leaves the range, below the minimum.
			name: "28-bit mid/side left negative overflow", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{-(1 << 27)}, n), 0, 28)
				writeVerbatim64(bw, slices.Repeat([]int64{-3}, n), 0, 29)
			},
		},
		{
			// The wide independent loop skips the narrowing copy for a bad subframe
			// and must still read the second one to reach the CRC-16.
			name: "28-bit independent stereo first subframe out of range", bits: 28, chCode: chIndependent2, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 28, []int64{1<<27 - 1}, step1(1))
				writeConstant64(bw, 0, 0, 28)
			},
		},
		{
			name: "28-bit right/side positive overflow", bits: 28, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{1}, n), 0, 29)
				writeVerbatim64(bw, slices.Repeat([]int64{1<<27 - 1}, n), 0, 28)
			},
		},
		{
			name: "28-bit right/side negative overflow", bits: 28, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{-1}, n), 0, 29)
				writeVerbatim64(bw, slices.Repeat([]int64{-(1 << 27)}, n), 0, 28)
			},
		},
		{
			name: "28-bit left/side negative overflow", bits: 28, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, slices.Repeat([]int64{-(1 << 27)}, n), 0, 28)
				writeVerbatim64(bw, slices.Repeat([]int64{1}, n), 0, 29)
			},
		},
		// The next nine reject rows leave every channel the output check inspects
		// in range, so only the subframe verdict (the bad flag from decodePair)
		// can reject the frame.
		{
			// Left is a subframe, never output-checked; side 40000 keeps right at 0.
			name: "left/side left subframe out of range, right in range", bits: 16, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 16, nil, spike64(3, 40000))
				writeVerbatim(bw, spike32(3, 40000), 0, 17)
			},
		},
		{
			name: "right/side right subframe out of range, left in range", bits: 16, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, spike32(3, -40000), 0, 17)
				writeRawFixed(bw, 0, 16, nil, spike64(3, 40000))
			},
		},
		{
			// 200 fits 16 bits but not the 8 effective bits. A decoder that ignored the
			// bad flag would rebuild l = r = 200 from sample 1, in range, because
			// subframe.go returns at its range check (subframe.go:103) before the
			// wasted-bits shift (subframe.go:118).
			name: "mid/side mid beyond effective width, derived in range", bits: 16, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 8, 8, []int64{0}, step1(200))
				writeVerbatim(bw, make([]int32, n), 0, 17)
			},
		},
		{
			// Side width is bps+1, so 8 wasted bits leave 9 effective bits; 400 is
			// beyond them yet the rebuilt l = 200 and r = -200 are inside 16 bits.
			name: "mid/side side beyond effective width, derived in range", bits: 16, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, make([]int32, n), 0, 16)
				writeRawFixed(bw, 8, 9, []int64{0}, step1(400))
			},
		},
		{
			name: "left/side side beyond effective width, derived in range", bits: 16, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim(bw, make([]int32, n), 0, 16)
				writeRawFixed(bw, 8, 9, []int64{0}, step1(400))
			},
		},
		{
			// The wide path (bps 28) has its own subframe verdict.
			name: "28-bit left/side left subframe out of range, right in range", bits: 28, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 0, 28, nil, spike64(3, 1<<27))
				writeVerbatim64(bw, spike64(3, 1<<27), 0, 29)
			},
		},
		{
			name: "28-bit right/side right subframe out of range, left in range", bits: 28, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, spike64(3, -(1<<27)), 0, 29)
				writeRawFixed(bw, 0, 28, nil, spike64(3, 1<<27))
			},
		},
		{
			name: "28-bit mid/side mid beyond effective width, derived in range", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeRawFixed(bw, 20, 8, []int64{0}, step1(200))
				writeVerbatim64(bw, zeros(n), 0, 29)
			},
		},
		{
			name: "28-bit left/side side beyond effective width, derived in range", bits: 28, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, zeros(n), 0, 28)
				writeRawFixed(bw, 20, 9, []int64{0}, step1(400))
			},
		},
		// The wide decorrelators must keep a bad sample seen early in the block; the
		// wide stereo rows above carry a constant bad block, where the last sample
		// alone gives the same verdict.
		{
			name: "28-bit left/side single early overflow", bits: 28, chCode: chLeftSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, zeros(n), 0, 28)
				writeVerbatim64(bw, spike64(0, -(1<<27)), 0, 29)
			},
		},
		{
			name: "28-bit right/side single early overflow", bits: 28, chCode: chRightSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, spike64(0, 1<<27), 0, 29)
				writeVerbatim64(bw, zeros(n), 0, 28)
			},
		},
		{
			// mid 2^27-1 and side 1 make left 2^27 at sample 0 only.
			name: "28-bit mid/side single early overflow", bits: 28, chCode: chMidSide, reject: true,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, spike64(0, 1<<27-1), 0, 28)
				writeVerbatim64(bw, spike64(0, 1), 0, 29)
			},
		},
		// Derived samples exactly on the wide limits -2^27 and 2^27-1 are accepted,
		// per decorrelation mode and per derived channel.
		{
			name: "28-bit left/side right at minimum", bits: 28, chCode: chLeftSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, zeros(n), 0, 28)
				writeVerbatim64(bw, rep64(1<<27), 0, 29)
			},
			want: wantConst(0, -(1 << 27)),
		},
		{
			name: "28-bit left/side right at maximum", bits: 28, chCode: chLeftSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, zeros(n), 0, 28)
				writeVerbatim64(bw, rep64(-(1<<27 - 1)), 0, 29)
			},
			want: wantConst(0, 1<<27-1),
		},
		{
			name: "28-bit right/side left at minimum", bits: 28, chCode: chRightSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, rep64(-(1 << 27)), 0, 29)
				writeVerbatim64(bw, zeros(n), 0, 28)
			},
			want: wantConst(-(1 << 27), 0),
		},
		{
			name: "28-bit right/side left at maximum", bits: 28, chCode: chRightSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, rep64(1<<27-1), 0, 29)
				writeVerbatim64(bw, zeros(n), 0, 28)
			},
			want: wantConst(1<<27-1, 0),
		},
		{
			name: "28-bit mid/side both at minimum", bits: 28, chCode: chMidSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, rep64(-(1 << 27)), 0, 28)
				writeVerbatim64(bw, zeros(n), 0, 29)
			},
			want: wantConst(-(1 << 27), -(1 << 27)),
		},
		{
			name: "28-bit mid/side both at maximum", bits: 28, chCode: chMidSide,
			build: func(bw *bitio.Writer) {
				writeVerbatim64(bw, rep64(1<<27-1), 0, 28)
				writeVerbatim64(bw, zeros(n), 0, 29)
			},
			want: wantConst(1<<27-1, 1<<27-1),
		},
	}
}

// TestDecodeRejectsOutOfRangeSamples feeds CRC-valid frames whose samples leave
// their bit depth and expects flac.ErrSampleOutOfRange, a verdict libFLAC 1.5.0
// reaches for plain out-of-range output (it compares the final output to the
// frame depth, stream_decoder.c lines 2457 to 2470). The rows "wasted bits wrap
// back in range" and the two 32-bit narrowing rows are rejected here but, from
// reading that code and not from running it, accepted by libFLAC. The
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
// so a bit flip in a real stream keeps its corruption verdict. One frame per
// place Decode defers the range verdict: independent narrow, stereo output
// check, wide independent, wide stereo, and the stereo subframe verdict on the
// narrow and wide paths.
func TestDecodeRangeVerdictAfterCRC(t *testing.T) {
	names := []string{
		"lpc narrow route", "left/side right out of range",
		"28-bit independent", "32-bit left/side narrowing wrap",
		"left/side left subframe out of range, right in range",
		"28-bit left/side left subframe out of range, right in range",
	}
	byName := map[string]rangeCase{}
	for _, rc := range rangeCases() {
		byName[rc.name] = rc
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			c, ok := byName[name]
			if !ok || !c.reject {
				t.Fatalf("fixture %q missing or not an out-of-range frame", name)
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
		})
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

package frame

import (
	"math/bits"

	"github.com/tphakala/go-flac/internal/bitio"
)

// wastedBits returns the number of low-order zero bits common to every sample.
// Returns 0 when all samples are zero (that case is encoded as a constant subframe).
func wastedBits(s []int32) int {
	var orAll int32
	for _, v := range s {
		orAll |= v
	}
	if orAll == 0 {
		return 0
	}
	return bits.TrailingZeros32(uint32(orAll))
}

// writeSubframeHeader writes the 1-bit zero pad, 6-bit type code, and the
// wasted-bits field. When wasted == 0 the flag bit is 0. When wasted > 0 the
// flag bit is 1 followed by a unary value of (wasted-1) zeros then a 1.
func writeSubframeHeader(bw *bitio.Writer, typeCode, wasted int) {
	bw.WriteBits(0, 1)
	bw.WriteBits(uint64(typeCode), 6)
	if wasted == 0 {
		bw.WriteBits(0, 1)
	} else {
		bw.WriteBits(1, 1)
		bw.WriteUnary(uint64(wasted - 1))
	}
}

// writeConstant writes a FLAC constant subframe. The decoder fills every sample
// slot with the single stored value then shifts left by wasted bits, so the
// encoder stores value>>wasted at bps-wasted bits.
func writeConstant(bw *bitio.Writer, value int32, wasted, bps int) {
	writeSubframeHeader(bw, 0, wasted)
	bw.WriteSignedBits(int64(value>>uint(wasted)), uint(bps-wasted))
}

// writeVerbatim writes a FLAC verbatim subframe. Each sample is stored
// right-shifted by wasted bits at bps-wasted bits. The decoder reads each
// stored sample and then shifts left by wasted to restore the original value.
func writeVerbatim(bw *bitio.Writer, s []int32, wasted, bps int) {
	writeSubframeHeader(bw, 1, wasted)
	eff := uint(bps - wasted)
	for _, v := range s {
		bw.WriteSignedBits(int64(v>>uint(wasted)), eff)
	}
}

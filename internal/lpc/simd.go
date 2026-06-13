package lpc

import "github.com/tphakala/go-flac/internal/i32"

// FixedResidualsDiff writes the FLAC fixed-predictor output of order `order` for
// src into dst in subframe layout: dst[0:order] receives the warmup samples
// verbatim and dst[order:len(src)] receives the residuals
//
//	dst[order+i] = src[order+i] - prediction   (i in [0, len(src)-order))
//
// so dst[order:] equals the residual slice ComputeFixedResiduals(_, src, order)
// would produce. dst must have length len(src) and MUST NOT alias src (the SIMD
// kernels read a forward window of src while writing dst).
//
// It dispatches to the SIMD i32.Diff kernels (AVX2 on amd64, NEON on arm64, pure
// Go elsewhere or for short inputs). Those kernels use int32 wraparound arithmetic
// that is bit-identical to ComputeFixedResiduals' int64-accumulate-then-truncate
// (both reduce mod 2^32), so the residuals, their Rice cost, and the encoded
// stream are unchanged whether or not SIMD is active.
//
// order must be in [1, 4]. Order 0 (residual == src) needs no differencing and is
// handled by the caller (it passes src straight through), so it is rejected here
// rather than silently copying.
func FixedResidualsDiff(dst, src []int32, order int) {
	switch order {
	case 1:
		i32.Diff1(dst, src)
	case 2:
		i32.Diff2(dst, src)
	case 3:
		i32.Diff3(dst, src)
	case 4:
		i32.Diff4(dst, src)
	default:
		panic("lpc: FixedResidualsDiff order out of range [1,4]")
	}
}

// LPCResidualsEncode writes the quantized-LPC residual of order len(qcoeff) for
// src into dst in subframe layout: dst[0:order] receives the warmup samples
// verbatim and dst[order:len(src)] receives the residuals
//
//	dst[order+i] = src[order+i] - int32((Σ_j qcoeff[j]*src[order+i-1-j]) >> shift)
//
// so dst[order:] equals the residual slice ComputeLPCResiduals(_, src, qcoeff,
// shift, order) would produce. dst must have length len(src) and MUST NOT alias
// src. shift is FLAC's quantization right-shift (0..31).
//
// It dispatches to the SIMD i32.LPCResidualEncode kernel (AVX2/NEON/pure Go),
// which accumulates the prediction in int64, arithmetic-shifts the full sum before
// narrowing to int32, and subtracts with int32 wraparound, bit-identical to
// ComputeLPCResiduals. The output therefore prices a candidate exactly as the
// scalar path would, so the order the planner picks is unchanged.
func LPCResidualsEncode(dst, src, qcoeff []int32, shift int) {
	i32.LPCResidualEncode(dst[:len(src)], src, qcoeff, uint(shift))
}

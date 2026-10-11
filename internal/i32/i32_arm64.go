//go:build arm64

package i32

import "github.com/tphakala/simd/cpu"

// NEON processes 4 int32 pairs (one .4S register) per iteration.
const minNEONElements = 4

var hasNEON = cpu.ARM64.NEON

func interleave2I32(dst, a, b []int32) {
	if hasNEON && len(a) >= minNEONElements {
		interleave2NEON(dst, a, b)
		return
	}
	interleave2Go(dst, a, b)
}

func deinterleave2I32(a, b, src []int32) {
	if hasNEON && len(a) >= minNEONElements {
		deinterleave2NEON(a, b, src)
		return
	}
	deinterleave2Go(a, b, src)
}

//go:noescape
func interleave2NEON(dst, a, b []int32)

//go:noescape
func deinterleave2NEON(a, b, src []int32)

func addI32(dst, a, b []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		addNEON(dst, a, b)
		return
	}
	addGo(dst, a, b)
}

func subI32(dst, a, b []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		subNEON(dst, a, b)
		return
	}
	subGo(dst, a, b)
}

func midSideEncodeI32(mid, side, left, right []int32) {
	if hasNEON && len(mid) >= minNEONElements {
		midSideEncodeNEON(mid, side, left, right)
		return
	}
	midSideEncodeGo(mid, side, left, right)
}

func midSideDecodeI32(left, right, mid, side []int32) {
	if hasNEON && len(left) >= minNEONElements {
		midSideDecodeNEON(left, right, mid, side)
		return
	}
	midSideDecodeGo(left, right, mid, side)
}

func diff1I32(dst, src []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		diff1NEON(dst, src)
		return
	}
	diff1Go(dst, src)
}

func diff2I32(dst, src []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		diff2NEON(dst, src)
		return
	}
	diff2Go(dst, src)
}

func diff3I32(dst, src []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		diff3NEON(dst, src)
		return
	}
	diff3Go(dst, src)
}

func diff4I32(dst, src []int32) {
	if hasNEON && len(dst) >= minNEONElements {
		diff4NEON(dst, src)
		return
	}
	diff4Go(dst, src)
}

//go:noescape
func addNEON(dst, a, b []int32)

//go:noescape
func subNEON(dst, a, b []int32)

//go:noescape
func midSideEncodeNEON(mid, side, left, right []int32)

//go:noescape
func midSideDecodeNEON(left, right, mid, side []int32)

func cumsumI32(a []int32) {
	if hasNEON && len(a) >= minNEONElements {
		cumsumNEON(a)
		return
	}
	cumsumGo(a)
}

//go:noescape
func cumsumNEON(a []int32)

//go:noescape
func diff1NEON(dst, src []int32)

//go:noescape
func diff2NEON(dst, src []int32)

//go:noescape
func diff3NEON(dst, src []int32)

//go:noescape
func diff4NEON(dst, src []int32)

// minNEONRestoreOrder is the smallest predictor order at which the SIMD decode
// recurrence kernel beats the specialized scalar kernels on NEON (tuned from the
// Raspberry Pi 5 benchmarks). Below it the serial dependency leaves too little
// per-output tap work to amortize the horizontal reduction. The scalar route
// takes precedence up to maxScalarRestoreOrder (see LPCRestore), so NEON is
// dispatched for orders [max(minNEONRestoreOrder, maxScalarRestoreOrder+1), 32].
const minNEONRestoreOrder = 6

// maxScalarRestoreOrder is the largest order LPCRestore routes to the pure-Go
// scalar kernels (lpc_small.go) before the NEON dispatch. Tuned from
// BenchmarkLPCRestoreOrder and BenchmarkLPCRestoreNEONOrder on a Raspberry Pi 5.
const maxScalarRestoreOrder = 5

// No order may fall between the scalar ceiling and the SIMD minimum, where it
// would take the slower lpcRestoreGo.
const _ = uint(maxScalarRestoreOrder + 1 - minNEONRestoreOrder)

// maxScalarRestoreOrderNarrow is the largest order LPCRestoreNarrow routes to the
// int32 scalar kernels (lpc_small_narrow.go); higher orders run LPCRestore. Tuned
// from BenchmarkLPCRestoreNarrowOrder on a Raspberry Pi 5 (min of 10 runs, ns per
// 1000 samples, int64 LPCRestore / int32 kernel):
//
//	order  1: 3002 / 2238    order  5: 6249 / 2322    order  9: 6859 / 3749
//	order  2: 3123 / 2217    order  6: 4861 / 2929    order 10: 5816 / 4160
//	order  3: 3778 / 2199    order  7: 5255 / 2991    order 11: 6225 / 4574
//	order  4: 5000 / 2118    order  8: 6127 / 3340    order 12: 6866 / 4977
//
// The int32 kernel wins by 25-63% at every order through 12, the highest kernel
// that exists (maxScalarKernelOrderNarrow). A NEON int32 kernel (MLA on .4S, one
// and two accumulators) was also measured: it lost to the int64 NEON kernel at
// orders 7-12 and did not win consistently above them, so there is none.
//
// Above 12 the int64 NEON kernel also beats the generic int32 loop
// (lpcRestoreGoNarrow), so the ceiling stays at 12. Min of 10 runs, ns per 1000
// samples, int64 LPCRestore / generic int32 loop:
//
//	order 13:  9925 / 14033    order 16:  9900 / 16457    order 19:  9884 / 18867
//	order 14:  8277 / 14843    order 17: 10711 / 17262    order 32: 12996 / 29129
//	order 15:  9086 / 15666    order 18:  9062 / 18066
//
// The generic loop is 41-124% slower from order 13 to 32 (p=0.000 at every
// measured order for 1000 and 4096 samples).
const maxScalarRestoreOrderNarrow = 12

// firstSIMDRestoreOrder backs FirstSIMDRestoreOrder: NEON takes orders
// [max(minNEONRestoreOrder, maxScalarRestoreOrder+1), maxLPCRestoreOrder].
func firstSIMDRestoreOrder() int {
	if !hasNEON {
		return 0
	}
	return max(minNEONRestoreOrder, maxScalarRestoreOrder+1)
}

func lpcResidualEncodeI32(res, samples, coeffs []int32, shift uint) {
	if hasNEON && len(res)-len(coeffs) >= minNEONElements {
		lpcResidualEncodeNEON(res, samples, coeffs, shift)
		return
	}
	lpcResidualEncodeGo(res, samples, coeffs, shift)
}

func lpcRestoreI32(out, residual, coeffs []int32, shift uint) {
	order := len(coeffs)
	if hasNEON && order >= minNEONRestoreOrder && order <= maxLPCRestoreOrder && len(out)-order >= 1 {
		// The kernel dots the ascending window with the coefficients reversed,
		// so reverse once into a stack array (no heap alloc; the array does not
		// escape through the //go:noescape kernel).
		var rc [maxLPCRestoreOrder]int32
		for k := range order {
			rc[k] = coeffs[order-1-k]
		}
		lpcRestoreNEON(out, residual, rc[:order], shift)
		return
	}
	// Without NEON LPCRestore still wants the specialized kernels for every
	// order that has one; lpcRestoreScalar falls back to lpcRestoreGo above them.
	lpcRestoreScalar(out, residual, coeffs, shift)
}

//go:noescape
func lpcResidualEncodeNEON(res, samples, coeffs []int32, shift uint)

//go:noescape
func lpcRestoreNEON(out, residual, rcoeffs []int32, shift uint)

// riceSumsI32 dispatches the Rice per-parameter unary-bit sums. The NEON kernel
// always writes the full riceParamCount (15) FLAC sums, so it is used only for
// that width; other widths and short inputs use the pure-Go reference.
func riceSumsI32(sums []uint64, res []int32) {
	if hasNEON && len(sums) == riceParamCount && len(res) >= minNEONElements {
		riceSumsNEON(sums, res)
		return
	}
	riceSumsGo(sums, res)
}

// zigzagSumI32 dispatches the residual zigzag-fold sum (RiceSums' k=0 column).
// The NEON kernel widens to int64 lanes; it gates on NEON and at least one full
// 4-element block, falling back to the pure-Go reference otherwise.
func zigzagSumI32(res []int32) uint64 {
	if hasNEON && len(res) >= minNEONElements {
		return zigzagSumNEON(res)
	}
	return zigzagSumGo(res)
}

//go:noescape
func zigzagSumNEON(res []int32) uint64

// fixedAbsSumsI32 dispatches the five fixed-predictor residual abs-sums. The
// NEON kernel computes the order-0..4 finite differences in int64 lanes via a
// windowed sign-extending cascade; it handles the first 4 warm-up samples and
// the tail scalar, so it only needs the warm-up samples to exist. Shorter inputs
// use the pure-Go reference.
func fixedAbsSumsI32(src []int32, sums *[5]uint64) {
	if hasNEON && len(src) >= minNEONElements {
		fixedAbsSumsNEON(src, sums)
		return
	}
	fixedAbsSumsGo(src, sums)
}

//go:noescape
func fixedAbsSumsNEON(src []int32, sums *[5]uint64)

//go:noescape
func riceSumsNEON(sums []uint64, res []int32)

// riceSumsWideI32 dispatches the FLAC 5-bit Rice sums (len(sums) ==
// riceMaxParam5+1 = 31). The NEON path reuses the 15-wide kernel for columns
// 0..14 and a high kernel for columns 15..30, so the whole range is vectorized
// instead of falling to the scalar tail; both gate on NEON and one full block.
func riceSumsWideI32(sums []uint64, res []int32) {
	// The exact-width gate mirrors riceSumsI32: riceSumsHighNEON is a fixed
	// 16-column writer, so only dispatch it when sums is the full 31-wide slice;
	// any other length goes to the pure-Go reference (which handles all widths).
	if hasNEON && len(sums) == riceMaxParam5+1 && len(res) >= minNEONElements {
		riceSumsNEON(sums[:riceParamCount], res)     // columns 0..14
		riceSumsHighNEON(sums[riceParamCount:], res) // columns 15..30
		return
	}
	riceSumsGo(sums, res)
}

//go:noescape
func riceSumsHighNEON(sums []uint64, res []int32)

// minMaxI32 dispatches the signed int32 min/max reduction. The NEON kernel does
// the reduction in 4-wide SMIN/SMAX lanes with a single-instruction SMINV/SMAXV
// across-vector fold and a scalar tail, so it gates on NEON and at least one full
// 4-element block; shorter slices use the pure-Go reference. res is non-empty
// (the public MinMax guards the empty case).
func minMaxI32(res []int32) (minVal, maxVal int32) {
	if hasNEON && len(res) >= minNEONElements {
		return minMaxNEON(res)
	}
	return minMaxGo(res)
}

//go:noescape
func minMaxNEON(res []int32) (minVal, maxVal int32)

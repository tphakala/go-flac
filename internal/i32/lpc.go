package i32

// Quantized-LPC residual encode and restore for the FLAC codec.
//
// FLAC's LPC subframes predict each sample from a fixed-point linear
// combination of the preceding `order` samples, using integer coefficients
// quantized to a shared right-shift. The encoder Rice-codes the residual
// LPCResidualEncode produces; the decoder reconstructs the samples with
// LPCRestore. The predictor order is taken from len(coeffs) (FLAC allows 1..32),
// and shift is the quantization right-shift applied to the prediction sum.
//
// Both functions lay the data out as a FLAC subframe stores it: the first
// `order` entries are the verbatim warm-up samples and the rest are the residual
// (encode) or the reconstructed samples (decode). The prediction sum is
// accumulated in int64 so it does not overflow for FLAC's coefficient precision
// and order; only the final shifted prediction is truncated to int32.
// LPCRestoreNarrow is the exception: it accumulates in int32, which is exact only
// when LPCRestoreNarrowSafe holds for the subframe's coefficients and sample
// width. Each
// function clamps to n = min of its two slice lengths, writes only into the
// caller's destination, and uses int32 wraparound. LPCResidualEncode's
// destination must not alias its source (the FIR reads a forward window of the
// source while writing). LPCRestore, being the serial recurrence, may be called
// fully in place with out and residual the same slice (exact aliasing): it reads
// residual[i] before storing out[i] at that index and otherwise reads only
// already-written outputs. Partial overlap remains unsupported.
//
// LPCResidualEncode is a FIR that vectorizes across output samples; LPCRestore
// is a serial recurrence (each output feeds the next prediction) and so is far
// less parallel. Both have SIMD kernels where they pay off, with a pure-Go
// fallback that is the bit-exact source of truth.

// maxLPCShift is the largest shift the kernels handle. A 64-bit accumulator
// shifted by 63 already saturates to 0 or -1, and the SIMD kernels' emulated
// arithmetic shift (and x86's shift-count masking) are only correct for counts
// in [0, 63], so the public functions clamp shift to this value.
const maxLPCShift = 63

// maxLPCRestoreOrder caps the predictor order the SIMD decode kernels accept. It
// matches FLAC's maximum LPC order (32) and sizes the stack array the dispatch
// reverses the coefficients into, so the reversal stays allocation-free. Orders
// above it fall back to the pure-Go recurrence.
const maxLPCRestoreOrder = 32

// FirstSIMDRestoreOrder returns the smallest predictor order LPCRestore
// dispatches to a SIMD decode kernel (AVX2 on amd64, NEON on arm64) on this
// build and CPU; every order from it through maxLPCRestoreOrder takes the
// SIMD kernel when the block is longer than the order (otherwise LPCRestore
// copies the residual). It returns 0 when no order does: builds without a SIMD restore
// kernel, or a CPU (or GODEBUG setting) without the required feature. It is
// derived from the same constants and CPU flag LPCRestore routes on, so tests
// can assert their reach against it.
func FirstSIMDRestoreOrder() int { return firstSIMDRestoreOrder() }

// maxLPCShiftNarrow is the largest shift the int32-accumulating kernels handle.
// For a sum that fits in int32, a shift of 31 or more already yields 0 or -1, and
// the 32-bit arithmetic shifts mask the count to 5 bits, so LPCRestoreNarrow
// clamps shift to this value before any narrow kernel.
const maxLPCShiftNarrow = 31

// LPCRestoreNarrowSafe reports whether a subframe of bps-bit samples with these
// coefficients and shift can be restored with 32-bit accumulation, LPCRestoreNarrow,
// without changing the result. It is libFLAC 1.5.0's condition (stream_decoder.c
// read_subframe_lpc_ with FLAC__lpc_max_residual_bps and
// FLAC__lpc_max_prediction_before_shift_bps in lpc.c): both
//
//	bits(2^(bps-1) * A) <= 32            (the unshifted prediction fits int32)
//	bits(2^(bps-1) + ceil(2^(bps-1) * A / 2^shift)) <= 32
//
// where A = sum(|coeffs[j]|) and bits is the signed bit width. In closed form,
// with A as an exact 64-bit sum: A < 2^(32-bps), and
// 2^(bps-1) + ceil(A * 2^(bps-1) / 2^shift) < 2^31.
//
// When it holds and every warm-up and restored sample lies in the bps-bit signed
// range, |sum(coeffs[j]*x)| <= 2^(bps-1) * A < 2^31 for any subset of terms, so
// the int32 sum cannot wrap in any summation order and equals the int64 sum. It
// returns false for bps == 0, bps > 32, shift > maxLPCShiftNarrow and more than
// maxLPCRestoreOrder coefficients.
func LPCRestoreNarrowSafe(coeffs []int32, bps, shift uint) bool {
	if bps == 0 || bps > 32 || shift > maxLPCShiftNarrow || len(coeffs) > maxLPCRestoreOrder {
		return false
	}
	var a uint64
	for _, c := range coeffs {
		a += uint64(max(int64(c), -int64(c))) // |MinInt32| = 2^31 fits
	}
	// Checked before shifting: a << (bps-1) must not overflow uint64.
	if a >= uint64(1)<<(32-bps) {
		return false
	}
	maxPred := a << (bps - 1)
	shifted := (maxPred + uint64(1)<<shift - 1) >> shift
	return uint64(1)<<(bps-1)+shifted < uint64(1)<<31
}

// LPCResidualEncode writes the quantized-LPC residual into res:
//
//	res[i] = samples[i]                                              for i < order
//	res[i] = samples[i] - int32((Σ_j coeffs[j]*samples[i-1-j]) >> shift) for i >= order
//
// where order = len(coeffs). It processes n = min(len(res), len(samples)) and
// leaves any trailing capacity in res untouched. An empty coeffs (order 0) or
// fewer than order+1 samples yields all warm-up (res is samples verbatim).
//
// shift is the quantization right-shift (FLAC uses 0..31). It is clamped to 63
// so the result stays consistent across the Go and SIMD paths: a shift of 63 or
// more of a 64-bit accumulator already yields 0 or -1, and the SIMD kernels'
// emulated shift is only valid for counts in [0, 63].
func LPCResidualEncode(res, samples, coeffs []int32, shift uint) {
	n := min(len(res), len(samples))
	if n == 0 {
		return
	}
	if order := len(coeffs); order == 0 || order >= n {
		copy(res[:n], samples[:n])
		return
	}
	if shift > maxLPCShift {
		shift = maxLPCShift
	}
	lpcResidualEncodeI32(res[:n], samples[:n], coeffs, shift)
}

// LPCRestore reconstructs the samples LPCResidualEncode encoded, inverting it
// with the serial recurrence:
//
//	out[i] = residual[i]                                            for i < order
//	out[i] = residual[i] + int32((Σ_j coeffs[j]*out[i-1-j]) >> shift)   for i >= order
//
// where order = len(coeffs). It processes n = min(len(out), len(residual)) and
// leaves any trailing capacity in out untouched. Given the same coeffs and shift,
// LPCRestore(LPCResidualEncode(samples)) == samples. shift is clamped to 63, as
// in LPCResidualEncode, so encode and decode stay matched across all paths.
//
// out and residual may be the same slice (exact aliasing), which the FLAC decode
// path relies on to reconstruct a subframe in place; partial overlap is not
// supported.
func LPCRestore(out, residual, coeffs []int32, shift uint) {
	n := min(len(out), len(residual))
	if n == 0 {
		return
	}
	order := len(coeffs)
	if order == 0 || order >= n {
		copy(out[:n], residual[:n])
		return
	}
	if shift > maxLPCShift {
		shift = maxLPCShift
	}
	if order <= maxScalarRestoreOrder {
		lpcRestoreScalar(out[:n], residual[:n], coeffs, shift)
		return
	}
	lpcRestoreI32(out[:n], residual[:n], coeffs, shift)
}

// LPCRestoreNarrow is LPCRestore with 32-bit accumulation: the prediction sum is
// computed in int32 and shifted, which is cheaper than int64 on some CPUs. It is
// bit-identical to LPCRestore only when LPCRestoreNarrowSafe(coeffs, bps, shift)
// holds for a bps that bounds every warm-up and restored sample, which is the case
// for a valid FLAC subframe. Otherwise the sum may wrap and the result may differ
// from LPCRestore, but it never panics or accesses memory out of bounds. It has
// the same n clamp, order == 0 / order >= n copy and aliasing contract as
// LPCRestore. Orders up to the per-architecture ceiling maxScalarRestoreOrderNarrow
// run the int32 scalar kernels (lpc_small_narrow.go); every other order runs
// LPCRestore, because no narrow SIMD kernel beat the int64 one in measurement.
func LPCRestoreNarrow(out, residual, coeffs []int32, shift uint) {
	n := min(len(out), len(residual))
	if n == 0 {
		return
	}
	order := len(coeffs)
	if order == 0 || order >= n {
		copy(out[:n], residual[:n])
		return
	}
	if order <= maxScalarRestoreOrderNarrow {
		lpcRestoreScalarNarrow(out[:n], residual[:n], coeffs, min(shift, maxLPCShiftNarrow))
		return
	}
	LPCRestore(out[:n], residual[:n], coeffs, shift)
}

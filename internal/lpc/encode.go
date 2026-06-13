package lpc

import "github.com/tphakala/go-flac/internal/i32"

// ComputeFixedResiduals fills res (len must be len(src)-order) with the order-N
// fixed-predictor residuals of src: res[i] = src[order+i] - prediction, where
// prediction = sum(fixedCoeffs[order][j] * src[order+i-1-j]). It is the exact
// inverse of RestoreFixed. Valid only for orders 0..4 and src bit depth <= 24,
// where residuals stay within int32.
//
// Each order is specialized with its constant coefficients so the compiler
// strength-reduces the predictor to shifts and adds (no runtime multiply) and
// drops the per-coefficient inner loop. Reslicing src to its exact window lets
// the indexed loads prove in-range. The int64 predictor and int32 truncation
// match the generic form exactly, so output is byte-identical.
func ComputeFixedResiduals(res, src []int32, order int) {
	switch order {
	case 0:
		copy(res, src[:len(res)])
	case 1:
		s := src[:len(res)+1]
		for i := range res {
			res[i] = s[i+1] - s[i]
		}
	case 2:
		s := src[:len(res)+2]
		for i := range res {
			res[i] = s[i+2] - int32(2*int64(s[i+1])-int64(s[i]))
		}
	case 3:
		s := src[:len(res)+3]
		for i := range res {
			res[i] = s[i+3] - int32(3*int64(s[i+2])-3*int64(s[i+1])+int64(s[i]))
		}
	case 4:
		s := src[:len(res)+4]
		for i := range res {
			res[i] = s[i+4] - int32(4*int64(s[i+3])-6*int64(s[i+2])+4*int64(s[i+1])-int64(s[i]))
		}
	default:
		coeffs := fixedCoeffs[order]
		for i := range res {
			n := order + i
			var pred int64
			for j, c := range coeffs {
				pred += c * int64(src[n-1-j])
			}
			res[i] = src[n] - int32(pred)
		}
	}
}

// ComputeFixedResiduals64 is the int64 analogue of ComputeFixedResiduals, used
// for sample depths above 24 bits. It is specialized per order on the same
// rationale: constant coefficients let the compiler strength-reduce the predictor
// and drop the inner loop, byte-identical to the generic accumulate.
func ComputeFixedResiduals64(res, src []int64, order int) {
	switch order {
	case 0:
		copy(res, src[:len(res)])
	case 1:
		s := src[:len(res)+1]
		for i := range res {
			res[i] = s[i+1] - s[i]
		}
	case 2:
		s := src[:len(res)+2]
		for i := range res {
			res[i] = s[i+2] - (2*s[i+1] - s[i])
		}
	case 3:
		s := src[:len(res)+3]
		for i := range res {
			res[i] = s[i+3] - (3*s[i+2] - 3*s[i+1] + s[i])
		}
	case 4:
		s := src[:len(res)+4]
		for i := range res {
			res[i] = s[i+4] - (4*s[i+3] - 6*s[i+2] + 4*s[i+1] - s[i])
		}
	default:
		coeffs := fixedCoeffs[order]
		for i := range res {
			n := order + i
			var pred int64
			for j, c := range coeffs {
				pred += c * src[n-1-j]
			}
			res[i] = src[n] - pred
		}
	}
}

// ComputeLPCResiduals computes the residuals for a quantized LPC predictor.
// It is the exact integer inverse of RestoreLPC: for each n = order+i,
//
//	pred   = sum_j int64(qcoeff[j]) * int64(src[n-1-j])
//	res[i] = src[n] - int32(pred >> shift)
//
// res must have length len(src)-order, and order must equal len(qcoeff).
// The accumulator is int64 and the arithmetic shift is applied to the full
// sum, matching RestoreLPC exactly so the decoder reconstructs bit-for-bit.
func ComputeLPCResiduals(res, src, qcoeff []int32, shift, order int) {
	for i := range res {
		n := order + i
		var pred int64
		for j, c := range qcoeff {
			pred += int64(c) * int64(src[n-1-j])
		}
		res[i] = src[n] - int32(pred>>uint(shift))
	}
}

// ComputeLPCResiduals64 is the int64 analogue of ComputeLPCResiduals and the exact
// integer inverse of RestoreLPC[int64].
func ComputeLPCResiduals64(res, src []int64, qcoeff []int32, shift, order int) {
	for i := range res {
		n := order + i
		var pred int64
		for j, c := range qcoeff {
			pred += int64(c) * src[n-1-j]
		}
		res[i] = src[n] - (pred >> uint(shift))
	}
}

// FixedAbsSums fills sums[order] with the sum of |residual| of the order-th fixed
// predictor (order 0..4), the order-th finite difference of src, in a single
// pass. The first `order` samples are warmup (excluded from coding) and excluded
// from the sum, matching FixedResidualsDiff(res, src, order) summed over
// res[order:]. This is the analysis input to EstimateRiceBits for fixed-order
// selection. It delegates to the SIMD-accelerated i32.FixedAbsSums (AVX2 on
// amd64, NEON on arm64, pure-Go fallback otherwise), which is bit-identical to
// the scalar int64 difference-cascade reference, so fixed-order selection is
// unchanged whether or not SIMD is active. Allocation-free (writes through the
// caller's array). The int64 wide-path analogue FixedAbsSums64 stays scalar.
func FixedAbsSums(src []int32, sums *[5]uint64) {
	i32.FixedAbsSums(src, sums)
}

// FixedAbsSums64 is the int64 analogue of FixedAbsSums: it fills sums[order] with
// the sum of |residual| of the order-th fixed predictor (order 0..4) in a single
// pass, excluding the first `order` warmup samples. The body is replaced by a SIMD
// kernel in a later task; this scalar form is the reference. Allocation-free.
func FixedAbsSums64(src []int64, sums *[5]uint64) {
	// Accumulate into a local array and store once at the end (see FixedAbsSums
	// for the register-residency rationale).
	var s [5]uint64
	var p1, p2, p3, p4 int64 // state: p1=prev e0, p2=prev e1, p3=prev e2, p4=prev e3
	for i, v := range src {
		e0 := v
		e1 := e0 - p1
		e2 := e1 - p2
		e3 := e2 - p3
		e4 := e3 - p4
		s[0] += absU64(e0)
		if i >= 1 {
			s[1] += absU64(e1)
		}
		if i >= 2 {
			s[2] += absU64(e2)
		}
		if i >= 3 {
			s[3] += absU64(e3)
		}
		if i >= 4 {
			s[4] += absU64(e4)
		}
		p1, p2, p3, p4 = e0, e1, e2, e3
	}
	*sums = s
}

// absU64 returns the magnitude of v as a uint64. It is correct for the entire
// int64 range, including math.MinInt64 (wrapping negation then a uint64 cast
// yields the right bit pattern). Callers feeding finite differences must ensure
// those differences do not overflow int64 first; for FLAC's max 32-bit samples
// the order-4 difference is bounded near 2^35, so this holds for FixedAbsSums
// and FixedAbsSums64 (whose int64 inputs are 32-bit sample values).
func absU64(v int64) uint64 {
	if v < 0 {
		return uint64(-v)
	}
	return uint64(v)
}

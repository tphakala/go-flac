package lpc

// Sample is the integer width used for decoded samples.
type Sample interface{ ~int32 | ~int64 }

// fixedCoeffs[order] holds the integer predictor coefficients for the FLAC fixed
// predictors. prediction = sum(coeff[i] * sample[n-1-i]).
var fixedCoeffs = [5][]int64{
	0: {},
	1: {1},
	2: {2, -1},
	3: {3, -3, 1},
	4: {4, -6, 4, -1},
}

// RestoreFixed reconstructs dst[order:] in place from residuals using the fixed
// predictor of the given order (0..4). dst[:order] holds warmup samples.
func RestoreFixed[T Sample](dst []T, order int) {
	coeffs := fixedCoeffs[order]
	for n := order; n < len(dst); n++ {
		var pred int64
		for i, c := range coeffs {
			pred += c * int64(dst[n-1-i])
		}
		dst[n] += T(pred)
	}
}

// RestoreLPC reconstructs dst[len(coeffs):] in place from residuals using the
// quantized linear predictor. shift is the right shift applied to the sum.
// order is len(coeffs); it is accepted here for call-site clarity.
func RestoreLPC[T Sample](dst []T, coeffs []int32, shift, order int) {
	_ = order // order == len(coeffs); kept for call-site clarity
	for n := len(coeffs); n < len(dst); n++ {
		var pred int64
		for i, c := range coeffs {
			pred += int64(c) * int64(dst[n-1-i])
		}
		dst[n] += T(pred >> uint(shift))
	}
}

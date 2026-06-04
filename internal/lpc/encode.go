package lpc

// ComputeFixedResiduals fills res (len must be len(src)-order) with the order-N
// fixed-predictor residuals of src: res[i] = src[order+i] - prediction, where
// prediction = sum(fixedCoeffs[order][j] * src[order+i-1-j]). It is the exact
// inverse of RestoreFixed. Valid only for orders 0..4 and src bit depth <= 24,
// where residuals stay within int32.
func ComputeFixedResiduals(res, src []int32, order int) {
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

// ComputeFixedResiduals64 is the int64 analogue of ComputeFixedResiduals.
func ComputeFixedResiduals64(res, src []int64, order int) {
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

// BestFixedOrder returns the fixed order in [0, maxOrder] (capped at 4 and at
// len(src)-1) whose residuals have the smallest sum of absolute values. Any order
// round-trips; this only chooses the most compressible one cheaply.
func BestFixedOrder(src []int32, maxOrder int) int {
	if maxOrder > 4 {
		maxOrder = 4
	}
	if maxOrder > len(src)-1 {
		maxOrder = len(src) - 1
	}
	if maxOrder < 0 {
		return 0
	}
	bestOrder, bestSum := 0, int64(-1)
	res := make([]int32, len(src))
	for order := range maxOrder + 1 {
		// FixedResidualsDiff writes [warmup | residual], so the residuals are
		// res[order:]; order 0's residual is src itself. The order this selects is
		// byte-identical to the scalar path because the residuals, and thus their
		// absolute-value sum, are bit-identical.
		var r []int32
		if order == 0 {
			r = src
		} else {
			FixedResidualsDiff(res, src, order)
			r = res[order:]
		}
		var sum int64
		for _, v := range r {
			if v < 0 {
				sum -= int64(v)
			} else {
				sum += int64(v)
			}
		}
		if bestSum < 0 || sum < bestSum {
			bestSum, bestOrder = sum, order
		}
	}
	return bestOrder
}

// BestFixedOrder64 is the int64 analogue of BestFixedOrder.
func BestFixedOrder64(src []int64, maxOrder int) int {
	if maxOrder > 4 {
		maxOrder = 4
	}
	if maxOrder > len(src)-1 {
		maxOrder = len(src) - 1
	}
	if maxOrder < 0 {
		return 0
	}
	bestOrder, bestSum := 0, int64(-1)
	res := make([]int64, len(src))
	for order := range maxOrder + 1 {
		r := res[:len(src)-order]
		ComputeFixedResiduals64(r, src, order)
		var sum int64
		for _, v := range r {
			if v < 0 {
				sum -= v
			} else {
				sum += v
			}
		}
		if bestSum < 0 || sum < bestSum {
			bestSum, bestOrder = sum, order
		}
	}
	return bestOrder
}

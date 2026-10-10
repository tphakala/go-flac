package i32

// lpcRestoreScalar is the pure-Go decode recurrence specialized for predictor
// orders 1..maxScalarRestoreOrder. It is bit-exact with lpcRestoreGo (same int64
// accumulation, arithmetic shift of the full sum, int32 narrowing and wraparound
// add) and keeps the same aliasing contract: out and residual may be the same
// slice, partial overlap is not supported. Other orders use lpcRestoreGo.
//
// The generic loop reloads out[i-1] from memory each sample and walks the taps
// in an inner loop. The kernels below hoist the coefficients and carry the last
// order outputs in registers, so the per-sample critical path is one multiply
// add chain and a shift. Each lpcRestoreN takes the destination and residual
// past the warm-up, the coefficients and the warm-up samples (the history seed),
// and in the loop x1 is the most recent output. The kernels are deliberately
// unrolled per order; the shift is already clamped to maxLPCShift, and the
// &63 in the loops only tells the compiler the count is in range.

// maxScalarRestoreOrder is the largest order with a specialized kernel. It is
// below the SIMD minimum orders (minLPCRestoreOrder, minNEONRestoreOrder, both 8),
// so the public LPCRestore routes these orders here before any SIMD dispatch.
const maxScalarRestoreOrder = 7

func lpcRestoreScalar(out, residual, coeffs []int32, shift uint) {
	order := len(coeffs)
	if order < 1 || order > maxScalarRestoreOrder {
		lpcRestoreGo(out, residual, coeffs, shift)
		return
	}
	n := len(out)
	if n <= order {
		copy(out, residual[:n])
		return
	}
	shift = min(shift, maxLPCShift) // same result as Go's >> for any count
	copy(out[:order], residual[:order])
	hist := out[:order]
	res := residual[order:n]
	dst := out[order:n]
	dst = dst[:len(res)]
	switch order {
	case 1:
		lpcRestore1(dst, res, coeffs, hist, shift)
	case 2:
		lpcRestore2(dst, res, coeffs, hist, shift)
	case 3:
		lpcRestore3(dst, res, coeffs, hist, shift)
	case 4:
		lpcRestore4(dst, res, coeffs, hist, shift)
	case 5:
		lpcRestore5(dst, res, coeffs, hist, shift)
	case 6:
		lpcRestore6(dst, res, coeffs, hist, shift)
	case 7:
		lpcRestore7(dst, res, coeffs, hist, shift)
	}
}

// lpcRestore1 is the order-1 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore1(dst, res, coeffs, hist []int32, shift uint) {
	c0 := int64(coeffs[0])
	x1 := int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c0 * x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x1 = int64(v)
	}
}

// lpcRestore2 is the order-2 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore2(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1 := int64(coeffs[0]), int64(coeffs[1])
	x1, x2 := int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x2, x1 = x1, int64(v)
	}
}

// lpcRestore3 is the order-3 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore3(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2 := int64(coeffs[0]), int64(coeffs[1]), int64(coeffs[2])
	x1, x2, x3 := int64(hist[2]), int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c2*x3 + c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x3, x2, x1 = x2, x1, int64(v)
	}
}

// lpcRestore4 is the order-4 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore4(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3 := int64(coeffs[0]), int64(coeffs[1]), int64(coeffs[2]), int64(coeffs[3])
	x1, x2, x3, x4 := int64(hist[3]), int64(hist[2]), int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x4, x3, x2, x1 = x3, x2, x1, int64(v)
	}
}

// lpcRestore5 is the order-5 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore5(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4 := int64(coeffs[0]), int64(coeffs[1]), int64(coeffs[2]), int64(coeffs[3]), int64(coeffs[4])
	x1, x2, x3, x4, x5 := int64(hist[4]), int64(hist[3]), int64(hist[2]), int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x5, x4, x3, x2, x1 = x4, x3, x2, x1, int64(v)
	}
}

// lpcRestore6 is the order-6 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore6(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5 := int64(coeffs[0]), int64(coeffs[1]), int64(coeffs[2]), int64(coeffs[3]), int64(coeffs[4]), int64(coeffs[5])
	x1, x2, x3, x4, x5, x6 := int64(hist[5]), int64(hist[4]), int64(hist[3]), int64(hist[2]), int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x6, x5, x4, x3, x2, x1 = x5, x4, x3, x2, x1, int64(v)
	}
}

// lpcRestore7 is the order-7 kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestore7(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6 := int64(coeffs[0]), int64(coeffs[1]), int64(coeffs[2]), int64(coeffs[3]), int64(coeffs[4]), int64(coeffs[5]), int64(coeffs[6])
	x1, x2, x3, x4, x5, x6, x7 := int64(hist[6]), int64(hist[5]), int64(hist[4]), int64(hist[3]), int64(hist[2]), int64(hist[1]), int64(hist[0])
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + int32(acc>>(shift&63))
		dst[i] = v
		x7, x6, x5, x4, x3, x2, x1 = x6, x5, x4, x3, x2, x1, int64(v)
	}
}

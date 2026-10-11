package i32

// Int32-accumulating variants of the lpc_small.go kernels, used by
// LPCRestoreNarrow for orders 1..maxScalarRestoreOrderNarrow. They compute the
// same recurrence as lpcRestoreScalar but multiply and add in int32, so they are
// bit-exact with it only under the LPCRestoreNarrowSafe precondition (the sum of
// |coeff| * 2^(bps-1) fits in int32 for every in-range history). The aliasing
// contract is the same: out and residual may be the same slice. The shift passed
// in must already be clamped to maxLPCShiftNarrow; the &31 in the loops only tells
// the compiler the count is in range. Orders 1..maxScalarKernelOrderNarrow have an
// unrolled kernel; a per-architecture ceiling above that runs lpcRestoreGoNarrow.

// maxScalarKernelOrderNarrow is the largest order with a specialized narrow
// kernel. Which orders LPCRestoreNarrow routes to int32 code is per architecture:
// maxScalarRestoreOrderNarrow in i32_amd64.go, i32_arm64.go, restore_order_arm.go
// and restore_order_other.go.
const maxScalarKernelOrderNarrow = 12

// A routing ceiling above what lpcRestoreGoNarrow handles would not compile.
const _ = uint(maxLPCRestoreOrder - maxScalarRestoreOrderNarrow)

// lpcRestoreScalarNarrow restores with the specialized narrow kernel for orders
// 1..maxScalarKernelOrderNarrow. Other orders run LPCRestore, so a direct call
// never lands on the generic loop when a faster int64 kernel exists. shift must be
// at most maxLPCShiftNarrow.
func lpcRestoreScalarNarrow(out, residual, coeffs []int32, shift uint) {
	order := len(coeffs)
	if order < 1 || order > maxScalarKernelOrderNarrow {
		LPCRestore(out, residual, coeffs, shift)
		return
	}
	n := len(out)
	if n <= order {
		copy(out, residual[:n])
		return
	}
	copy(out[:order], residual[:order])
	hist := out[:order]
	res := residual[order:n]
	dst := out[order:n]
	dst = dst[:len(res)]
	switch order {
	case 1:
		lpcRestoreNarrow1(dst, res, coeffs, hist, shift)
	case 2:
		lpcRestoreNarrow2(dst, res, coeffs, hist, shift)
	case 3:
		lpcRestoreNarrow3(dst, res, coeffs, hist, shift)
	case 4:
		lpcRestoreNarrow4(dst, res, coeffs, hist, shift)
	case 5:
		lpcRestoreNarrow5(dst, res, coeffs, hist, shift)
	case 6:
		lpcRestoreNarrow6(dst, res, coeffs, hist, shift)
	case 7:
		lpcRestoreNarrow7(dst, res, coeffs, hist, shift)
	case 8:
		lpcRestoreNarrow8(dst, res, coeffs, hist, shift)
	case 9:
		lpcRestoreNarrow9(dst, res, coeffs, hist, shift)
	case 10:
		lpcRestoreNarrow10(dst, res, coeffs, hist, shift)
	case 11:
		lpcRestoreNarrow11(dst, res, coeffs, hist, shift)
	case 12:
		lpcRestoreNarrow12(dst, res, coeffs, hist, shift)
	}
}

// lpcRestoreNarrow1 is the order-1 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow1(dst, res, coeffs, hist []int32, shift uint) {
	c0 := coeffs[0]
	x1 := hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c0 * x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x1 = v
	}
}

// lpcRestoreNarrow2 is the order-2 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow2(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1 := coeffs[0], coeffs[1]
	x1, x2 := hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x2, x1 = x1, v
	}
}

// lpcRestoreNarrow3 is the order-3 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow3(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2 := coeffs[0], coeffs[1], coeffs[2]
	x1, x2, x3 := hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x3, x2, x1 = x2, x1, v
	}
}

// lpcRestoreNarrow4 is the order-4 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow4(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3 := coeffs[0], coeffs[1], coeffs[2], coeffs[3]
	x1, x2, x3, x4 := hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x4, x3, x2, x1 = x3, x2, x1, v
	}
}

// lpcRestoreNarrow5 is the order-5 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow5(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4]
	x1, x2, x3, x4, x5 := hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x5, x4, x3, x2, x1 = x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow6 is the order-6 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow6(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5]
	x1, x2, x3, x4, x5, x6 := hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x6, x5, x4, x3, x2, x1 = x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow7 is the order-7 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow7(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6]
	x1, x2, x3, x4, x5, x6, x7 := hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x7, x6, x5, x4, x3, x2, x1 = x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow8 is the order-8 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow8(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6, c7 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6], coeffs[7]
	x1, x2, x3, x4, x5, x6, x7, x8 := hist[7], hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c7*x8 + c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x8, x7, x6, x5, x4, x3, x2, x1 = x7, x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow9 is the order-9 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow9(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6, c7, c8 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6], coeffs[7], coeffs[8]
	x1, x2, x3, x4, x5, x6, x7, x8, x9 := hist[8], hist[7], hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c8*x9 + c7*x8 + c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x9, x8, x7, x6, x5, x4, x3, x2, x1 = x8, x7, x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow10 is the order-10 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow10(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6, c7, c8, c9 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6], coeffs[7], coeffs[8], coeffs[9]
	x1, x2, x3, x4, x5, x6, x7, x8, x9, x10 := hist[9], hist[8], hist[7], hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c9*x10 + c8*x9 + c7*x8 + c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x10, x9, x8, x7, x6, x5, x4, x3, x2, x1 = x9, x8, x7, x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow11 is the order-11 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow11(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6, c7, c8, c9, c10 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6], coeffs[7], coeffs[8], coeffs[9], coeffs[10]
	x1, x2, x3, x4, x5, x6, x7, x8, x9, x10, x11 := hist[10], hist[9], hist[8], hist[7], hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c10*x11 + c9*x10 + c8*x9 + c7*x8 + c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x11, x10, x9, x8, x7, x6, x5, x4, x3, x2, x1 = x10, x9, x8, x7, x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreNarrow12 is the order-12 narrow kernel.
//
//nolint:dupl // intentional: order-specialized unrolled restore kernels
func lpcRestoreNarrow12(dst, res, coeffs, hist []int32, shift uint) {
	c0, c1, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4], coeffs[5], coeffs[6], coeffs[7], coeffs[8], coeffs[9], coeffs[10], coeffs[11]
	x1, x2, x3, x4, x5, x6, x7, x8, x9, x10, x11, x12 := hist[11], hist[10], hist[9], hist[8], hist[7], hist[6], hist[5], hist[4], hist[3], hist[2], hist[1], hist[0]
	dst = dst[:len(res)]
	for i, r := range res {
		acc := c11*x12 + c10*x11 + c9*x10 + c8*x9 + c7*x8 + c6*x7 + c5*x6 + c4*x5 + c3*x4 + c2*x3 + c1*x2 + c0*x1
		v := r + acc>>(shift&31)
		dst[i] = v
		x12, x11, x10, x9, x8, x7, x6, x5, x4, x3, x2, x1 = x11, x10, x9, x8, x7, x6, x5, x4, x3, x2, x1, v
	}
}

// lpcRestoreGoNarrow is the generic int32-accumulating restore for any order
// 1..maxLPCRestoreOrder, used by LPCRestoreNarrow above the unrolled kernels
// where the architecture's ceiling allows it (32-bit ARM). The contract is that
// of lpcRestoreScalarNarrow. The coefficients are reversed once into a stack
// array so the tap loop walks a forward window of out.
func lpcRestoreGoNarrow(out, residual, coeffs []int32, shift uint) {
	order := len(coeffs)
	if order < 1 || order > maxLPCRestoreOrder {
		LPCRestore(out, residual, coeffs, shift)
		return
	}
	n := len(out)
	if n <= order {
		copy(out, residual[:n])
		return
	}
	copy(out[:order], residual[:order])
	var rc [maxLPCRestoreOrder]int32
	for k := range order {
		rc[k] = coeffs[order-1-k]
	}
	rcs := rc[:order]
	for i := order; i < n; i++ {
		w := out[i-order : i]
		w = w[:len(rcs)]
		var acc int32
		for k, c := range rcs {
			acc += c * w[k]
		}
		out[i] = residual[i] + acc>>(shift&31)
	}
}

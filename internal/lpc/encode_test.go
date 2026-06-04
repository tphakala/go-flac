package lpc

import "testing"

func TestFixedResidualRoundTrip(t *testing.T) {
	signals := [][]int32{
		{5, 5, 5, 5, 5, 5, 5, 5},                // constant
		{0, 1, 2, 3, 4, 5, 6, 7},                // ramp (order 1 -> constant residual)
		{0, 1, 4, 9, 16, 25, 36, 49},            // quadratic (order 2)
		{-3, 7, -11, 13, -17, 19, -23, 29, -31}, // noisy
	}
	for _, src := range signals {
		for order := 0; order <= 4 && order < len(src); order++ {
			res := make([]int32, len(src)-order)
			ComputeFixedResiduals(res, src, order)
			dst := make([]int32, len(src))
			copy(dst[:order], src[:order])
			copy(dst[order:], res)
			RestoreFixed(dst, order)
			for i := range src {
				if dst[i] != src[i] {
					t.Fatalf("order %d: dst[%d]=%d, want %d", order, i, dst[i], src[i])
				}
			}
		}
	}
}

func TestFixedAbsSumsMatchesPerOrder(t *testing.T) {
	x := make([]int32, 1024)
	s := uint32(7)
	for i := range x {
		s = s*1103515245 + 12345
		x[i] = int32(s>>16) % 4096
	}
	var got [5]uint64
	FixedAbsSums(x, &got)
	// Reference: difference each order independently and sum abs over res[order:].
	for order := range 5 {
		var want uint64
		if order == 0 {
			for _, v := range x {
				want += absU64(int64(v))
			}
		} else {
			res := make([]int32, len(x))
			FixedResidualsDiff(res, x, order)
			for _, v := range res[order:] {
				want += absU64(int64(v))
			}
		}
		if got[order] != want {
			t.Fatalf("order %d: FixedAbsSums=%d want %d", order, got[order], want)
		}
	}
}

func TestFixedAbsSumsZeroAlloc(t *testing.T) {
	x := make([]int32, 4096)
	var sums [5]uint64
	if a := testing.AllocsPerRun(100, func() { FixedAbsSums(x, &sums) }); a != 0 {
		t.Fatalf("FixedAbsSums allocated %v times", a)
	}
}

func TestBestFixedOrderPicksLowResidual(t *testing.T) {
	// Linear ramp: second differences are all zero, so order 2 achieves sum=0 and wins.
	ramp := []int32{0, 10, 20, 30, 40, 50, 60, 70}
	if got := BestFixedOrder(ramp, 4); got != 2 {
		t.Fatalf("BestFixedOrder(ramp)=%d, want 2", got)
	}

	// Constant: order 0 residuals are all 9 (sum=54), order 1 residuals are all 0 (sum=0).
	// The function must pick order 1 as the first order achieving the minimum.
	constant := []int32{9, 9, 9, 9, 9, 9}
	if got := BestFixedOrder(constant, 4); got != 1 {
		t.Fatalf("BestFixedOrder(constant)=%d, want 1", got)
	}

	// Alternating-step signal: first differences alternate [+10, -1, +10, -1, ...].
	// Order 1 sum=43 is less than order 0 sum=148 and order 2 sum=66, so order 1 wins.
	alt := []int32{0, 10, 9, 19, 18, 28, 27, 37}
	if got := BestFixedOrder(alt, 4); got != 1 {
		t.Fatalf("BestFixedOrder(alt)=%d, want 1", got)
	}
}

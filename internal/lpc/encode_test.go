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

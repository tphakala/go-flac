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

// computeFixedResidualsRef is the original generic-coefficient-loop form, kept as
// a byte-identical oracle for the order-specialized ComputeFixedResiduals. Both
// accumulate the predictor in int64 and truncate to int32, so they must agree for
// every int32 input regardless of overflow.
func computeFixedResidualsRef(res, src []int32, order int) {
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

//nolint:dupl // intentional: typed parallel of TestComputeFixedResiduals64MatchesReference
func TestComputeFixedResidualsMatchesReference(t *testing.T) {
	var st uint32 = 0x9E3779B9
	next := func() int32 {
		st = st*1664525 + 1013904223
		return int32(st) //nolint:gosec // intentional wraparound to span the int32 range
	}
	// Lengths straddle any unroll factor and include the minimum per order.
	for _, length := range []int{4, 5, 6, 7, 8, 9, 16, 17, 31, 32, 33, 64, 100, 1023, 1024, 4096, 4097} {
		src := make([]int32, length)
		for i := range src {
			src[i] = next()
		}
		for order := 0; order <= 4 && order < length; order++ {
			want := make([]int32, length-order)
			got := make([]int32, length-order)
			computeFixedResidualsRef(want, src, order)
			ComputeFixedResiduals(got, src, order)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("order=%d len=%d i=%d: got %d, want %d", order, length, i, got[i], want[i])
				}
			}
		}
	}
}

func computeFixedResiduals64Ref(res, src []int64, order int) {
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

//nolint:dupl // intentional: typed parallel of TestComputeFixedResidualsMatchesReference
func TestComputeFixedResiduals64MatchesReference(t *testing.T) {
	var st uint64 = 0x123456789ABCDEF0
	next := func() int64 {
		st = st*6364136223846793005 + 1442695040888963407
		return int64(st) //nolint:gosec // intentional wraparound to span the int64 range
	}
	for _, length := range []int{4, 5, 6, 7, 8, 9, 16, 17, 31, 32, 33, 64, 100, 1023, 1024, 4096, 4097} {
		src := make([]int64, length)
		for i := range src {
			src[i] = next()
		}
		for order := 0; order <= 4 && order < length; order++ {
			want := make([]int64, length-order)
			got := make([]int64, length-order)
			computeFixedResiduals64Ref(want, src, order)
			ComputeFixedResiduals64(got, src, order)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("order=%d len=%d i=%d: got %d, want %d", order, length, i, got[i], want[i])
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

func TestFixedAbsSumsMatchesPerOrderEdgeCases(t *testing.T) {
	// Exercises the cases a SIMD kernel must reproduce: short lengths where the
	// order-k warmup exclusion (order k skips the first k samples) is the whole
	// behavior, lengths straddling the vector width for tail handling, and
	// magnitudes near the 24-bit domain (the encoder's int32 fixed-residual range,
	// where FixedResidualsDiff is a valid independent reference). Run under
	// GODEBUG=cpu.avx2=off too to assert SIMD/pure-Go parity.
	const lim = int32(1) << 23 // 24-bit signed domain: residuals stay within int32
	lengths := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 33, 64}
	s := uint32(0x12345)
	for _, n := range lengths {
		x := make([]int32, n)
		for i := range x {
			s = s*1103515245 + 12345
			x[i] = int32(s%uint32(2*lim)) - lim // uniform over [-2^23, 2^23)
		}
		if n >= 2 { // force the extremes of the valid domain to the endpoints
			x[0] = lim - 1
			x[n-1] = -lim
		}
		var got [5]uint64
		FixedAbsSums(x, &got)
		for order := range 5 {
			var want uint64
			if order == 0 {
				for _, v := range x {
					want += absU64(int64(v))
				}
			} else if order < len(x) {
				res := make([]int32, len(x))
				FixedResidualsDiff(res, x, order)
				for _, v := range res[order:] {
					want += absU64(int64(v))
				}
			}
			// order >= len(x): no residuals are coded, so want stays 0.
			if got[order] != want {
				t.Fatalf("n=%d order %d: FixedAbsSums=%d want %d", n, order, got[order], want)
			}
		}
	}
}

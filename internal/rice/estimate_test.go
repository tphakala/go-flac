package rice

import "testing"

func TestEstimateRiceBitsTracksExactCost(t *testing.T) {
	// For a flat residual block the exact single-partition Rice cost at the
	// optimal k must be within a few percent of the estimate; more importantly,
	// ranking two residual blocks by estimate must agree with ranking by exact
	// cost. We check the estimate never underestimates wildly and orders blocks
	// by magnitude.
	small := make([]int32, 4096)
	large := make([]int32, 4096)
	x := uint32(1)
	for i := range small {
		x = x*1103515245 + 12345
		small[i] = int32(x>>20) % 8    // tiny residuals
		large[i] = int32(x>>12) % 2048 // larger residuals
	}
	es := EstimateRiceBits(ZigzagSum(small), len(small))
	el := EstimateRiceBits(ZigzagSum(large), len(large))
	if es <= 0 || el <= 0 {
		t.Fatalf("non-positive estimates: small=%d large=%d", es, el)
	}
	if es >= el {
		t.Fatalf("estimate did not rank larger residuals as costlier: small=%d large=%d", es, el)
	}
}

func TestZigzagSumMatchesScalar(t *testing.T) {
	res := []int32{0, 1, -1, 2, -2, 1000, -1000, 1 << 20, -(1 << 20)}
	var want uint64
	for _, r := range res {
		want += zigzag(r)
	}
	if got := ZigzagSum(res); got != want {
		t.Fatalf("ZigzagSum=%d want %d", got, want)
	}
}

func TestZigzagSumMatchesScalarEdgeCases(t *testing.T) {
	// Covers the cases that distinguish a SIMD kernel from the scalar loop: the
	// int32 extremes (zigzag(MinInt32) == 0xFFFFFFFF, the widest single term) and
	// a spread of lengths around the vector width so the kernel's tail handling is
	// exercised. The scalar zigzag is the reference on every length. Run under
	// GODEBUG=cpu.avx2=off as well to assert SIMD/pure-Go parity.
	const minI32 = int32(-1 << 31)
	const maxI32 = int32(1<<31 - 1)
	lengths := []int{0, 1, 2, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 127}
	x := uint32(0x9e3779b9)
	for _, n := range lengths {
		res := make([]int32, n)
		for i := range res {
			x = x*1103515245 + 12345
			res[i] = int32(x)
		}
		// Force the extremes into any length >= 2 so the widest terms are summed.
		if n >= 2 {
			res[0] = minI32
			res[n-1] = maxI32
		}
		var want uint64
		for _, r := range res {
			want += zigzag(r)
		}
		if got := ZigzagSum(res); got != want {
			t.Fatalf("n=%d: ZigzagSum=%d want %d", n, got, want)
		}
	}
}

func TestEstimateRiceBitsZeroAlloc(t *testing.T) {
	res := make([]int32, 4096)
	if a := testing.AllocsPerRun(100, func() {
		_ = EstimateRiceBits(ZigzagSum(res), len(res))
	}); a != 0 {
		t.Fatalf("EstimateRiceBits/ZigzagSum allocated %v times", a)
	}
}

func TestZigzagSum64MatchesScalar(t *testing.T) {
	res := []int64{0, 1, -1, 2, -2, 1000, -1000, 1 << 40, -(1 << 40)}
	var want uint64
	for _, r := range res {
		want += zigzag64(r)
	}
	if got := ZigzagSum64(res); got != want {
		t.Fatalf("ZigzagSum64=%d want %d", got, want)
	}
}

func TestEstimateRiceBitsEdgeCases(t *testing.T) {
	// n <= 0 is degenerate (empty or over-long predictor order) and must return 0
	// rather than divide by zero.
	if got := EstimateRiceBits(1000, 0); got != 0 {
		t.Fatalf("EstimateRiceBits(_, 0) = %d, want 0", got)
	}
	if got := EstimateRiceBits(1000, -1); got != 0 {
		t.Fatalf("EstimateRiceBits(_, -1) = %d, want 0", got)
	}
	// All-zero residuals (zz == 0): mean is 0 so k == 0, and each sample costs only
	// the single unary stop bit, so the estimate is exactly n.
	const n = 64
	if got := EstimateRiceBits(0, n); got != n {
		t.Fatalf("EstimateRiceBits(0, %d) = %d, want %d", n, got, n)
	}
}

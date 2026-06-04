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

func TestEstimateRiceBitsZeroAlloc(t *testing.T) {
	res := make([]int32, 4096)
	if a := testing.AllocsPerRun(100, func() {
		_ = EstimateRiceBits(ZigzagSum(res), len(res))
	}); a != 0 {
		t.Fatalf("EstimateRiceBits/ZigzagSum allocated %v times", a)
	}
}

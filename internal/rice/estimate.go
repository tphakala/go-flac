package rice

import "math/bits"

// ZigzagSum returns the sum over j of zigzag(res[j]). zigzag maps signed
// residuals to the unsigned magnitudes the Rice coder counts (zigzag(r) is
// approximately 2|r|). Pure-Go reference; a SIMD kernel replaces the body in a
// later task. Allocation-free.
func ZigzagSum(res []int32) uint64 {
	var s uint64
	for _, r := range res {
		s += zigzag(r)
	}
	return s
}

// EstimateRiceBits approximates the Rice-coded size, in bits, of n residuals
// whose zigzag values sum to zz, evaluated at the cost-minimizing parameter k.
// The Rice cost of one partition is n*(1+k) + sum(zigzag>>k); over a whole block
// the mean zigzag is zz/n, so the optimum is near k* = floor(log2(zz/n)). This
// is used ONLY to rank predictor candidates: the chosen subframe is always
// priced exactly by PlanResidualInt32 before it is written, so the approximation
// affects compression (rarely, slightly) but never correctness. Allocation-free.
func EstimateRiceBits(zz uint64, n int) int {
	if n <= 0 {
		return 0
	}
	k := 0
	if mean := zz / uint64(n); mean > 0 {
		k = bits.Len64(mean) - 1
	}
	return n*(1+k) + int(zz>>uint(k))
}

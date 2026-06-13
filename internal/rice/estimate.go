package rice

import (
	"math/bits"

	"github.com/tphakala/go-flac/internal/i32"
)

// ZigzagSum64 returns the sum over j of zigzag64(res[j]); the int64 analogue of
// ZigzagSum, used to rank wide-path (25-32 bps) candidates. Allocation-free.
func ZigzagSum64(res []int64) uint64 {
	var s uint64
	for _, r := range res {
		s += zigzag64(r)
	}
	return s
}

// ZigzagSum returns the sum over j of zigzag(res[j]). zigzag maps signed
// residuals to the unsigned magnitudes the Rice coder counts (zigzag(r) is
// approximately 2|r|). It delegates to the SIMD-accelerated i32.ZigzagSum (AVX2
// on amd64, NEON on arm64, pure-Go fallback otherwise), which is bit-identical to
// the scalar Σ zigzag(res) reference, so the candidate ranking it feeds is
// unchanged whether or not SIMD is active. Allocation-free.
func ZigzagSum(res []int32) uint64 {
	return i32.ZigzagSum(res)
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

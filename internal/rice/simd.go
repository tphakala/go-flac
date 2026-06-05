package rice

import "github.com/tphakala/simd/i32"

// partitionSums fills sums[k] with the per-Rice-parameter unary-bit total
//
//	sums[k] = Σ_j (zigzag(res[j]) >> k)   for k in [0, len(sums))
//
// where zigzag(r) = uint32((r<<1) ^ (r>>31)). This is the data-dependent term of
// the Rice cost model bits(k) = sums[k] + n*(k+1); the finest-order partition
// planner builds one such table per partition (see buildFinestTablesInt32).
//
// It delegates to the SIMD-accelerated i32.RiceSums, which picks an AVX2 (amd64)
// or NEON (arm64) kernel at runtime and falls back to pure Go on other CPUs or for
// partitions shorter than the vector width. i32.RiceSums vectorizes FLAC's full
// parameter range: the 4-bit method (len(sums) <= 15) and the 5-bit method (up to
// maxParam5+1 = 31 columns). buildFinestTablesInt32 never requests more than
// maxParam5+1 columns, so the whole sweep, including the high columns that loud
// blocks need, stays on the kernel (earlier versions ran a scalar tail above 14).
// The result is byte-identical to the scalar accumulation from zero, so the Rice
// plans the encoder chooses, and the bytes it writes, are unchanged whether or not
// SIMD is active. sums is fully overwritten (not accumulated into); res is
// read-only; the call allocates nothing.
func partitionSums(sums []uint64, res []int32) {
	i32.RiceSums(sums, res)
}

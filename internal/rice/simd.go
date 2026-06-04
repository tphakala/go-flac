package rice

import "github.com/tphakala/simd/i32"

// simdParamCount is the width of i32.RiceSums' vectorized kernel: it computes the
// per-parameter sums for Rice parameters k = 0..14 (FLAC's 4-bit method range,
// 15 columns) with SIMD and serves any wider request from pure Go. partitionSums
// uses it as the split point between the SIMD kernel and its scalar tail. It
// equals maxParam4 + 1 by construction.
const simdParamCount = maxParam4 + 1

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
// partitions shorter than the vector width. The result is byte-identical to the
// scalar accumulation from zero, so the Rice plans the encoder chooses, and the
// bytes it writes, are unchanged whether or not SIMD is active. sums is fully
// overwritten (not accumulated into); res is read-only; the call allocates nothing.
//
// i32.RiceSums only vectorizes parameter counts up to simdParamCount (15, FLAC's
// 4-bit Rice range); a request for more columns falls entirely to pure Go. Loud
// blocks need columns above 14, which would send the whole hot loop down the
// scalar path. To keep the common low columns on AVX2, the wide case computes
// columns [0,15) with the SIMD kernel and only the few high columns [15,len) with
// a scalar tail. Both halves are bit-identical to Σ zigzag(res)>>k.
func partitionSums(sums []uint64, res []int32) {
	if len(sums) <= simdParamCount {
		i32.RiceSums(sums, res)
		return
	}
	i32.RiceSums(sums[:simdParamCount], res) // columns 0..14 on the SIMD kernel
	hi := sums[simdParamCount:]              // columns 15..len-1, scalar tail
	clear(hi)                                // i32.RiceSums overwrote only the low columns
	for _, r := range res {
		// Column simdParamCount+j wants zigzag(r) >> (simdParamCount+j). Pre-shift by
		// simdParamCount once, then a constant >>1 per column walks the remaining
		// shifts: this replaces the per-iteration variable shift (and its x86 CL
		// register setup plus the < 64 guard) with an add. Bit-identical.
		u := zigzag(r) >> simdParamCount
		for j := range hi {
			hi[j] += u
			u >>= 1
		}
	}
}

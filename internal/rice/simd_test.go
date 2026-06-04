package rice

import (
	"math/bits"
	"math/rand"
	"testing"
)

// refBuildFinestTablesInt32 is the pre-SIMD scalar implementation of
// buildFinestTablesInt32, kept verbatim as the parity oracle. The production
// function delegates the per-partition sums to i32.RiceSums (AVX2/NEON/pure-Go)
// and derives maxU from the residual extremes; this reference accumulates both
// the slow, obvious way. The two must agree bit-for-bit for every input, which is
// what guarantees the encoded FLAC stream is unchanged by the SIMD path.
func refBuildFinestTablesInt32(sc *Scratch, res []int32, blockSize, predOrder, pmax, ncols int) {
	P := 1 << pmax
	partLen := blockSize >> pmax
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		row := sc.sums[p*ncols : p*ncols+ncols]
		var mu uint64
		for j := range n {
			u := zigzag(res[idx+j])
			if u > mu {
				mu = u
			}
			for k := range row {
				row[k] += u >> (uint(k) & 63)
			}
		}
		sc.maxU[p] = mu
		idx += n
	}
}

// ncolsFor reproduces the column-count derivation in PlanResidualInt32 so the
// parity test exercises buildFinestTablesInt32 with exactly the (pmax, ncols) the
// encoder would use for res.
func ncolsFor(res []int32) int {
	var globalMaxU uint64
	if len(res) > 0 {
		lo, hi := res[0], res[0]
		for _, r := range res {
			if r < lo {
				lo = r
			}
			if r > hi {
				hi = r
			}
		}
		globalMaxU = max(zigzag(lo), zigzag(hi))
	}
	kHi := bits.Len64(globalMaxU) + 2
	if kHi > maxParam5 {
		kHi = maxParam5
	}
	return kHi + 1
}

func TestBuildFinestTablesInt32_SIMDParity(t *testing.T) {
	type sigKind int
	const (
		quiet  sigKind = iota // tiny residuals -> ncols well below 15
		mid                   // ncols straddles the 15-wide SIMD kernel width
		loud                  // large residuals -> ncols > 15, pure-Go fallback
		allneg                // exercises zigzag of the most-negative extreme
	)
	cases := []struct {
		blockSize, predOrder, maxPO int
		kind                        sigKind
	}{
		{4096, 2, 8, mid},
		{4096, 0, 8, quiet},
		{4096, 4, 6, loud},
		{4096, 8, 8, allneg},
		{4608, 2, 7, mid},  // non-power-of-two block (44100/... style)
		{512, 1, 4, quiet}, // small block, small partitions at pmax
		{4096, 2, 0, loud}, // pmax forced to 0: single partition
		{2048, 3, 8, mid},
	}
	rng := rand.New(rand.NewSource(0xF1AC))
	for ci, tc := range cases {
		res := make([]int32, tc.blockSize-tc.predOrder)
		for i := range res {
			switch tc.kind {
			case quiet:
				res[i] = int32(rng.Intn(7) - 3)
			case mid:
				res[i] = int32(rng.Intn(8001) - 4000)
			case loud:
				res[i] = rng.Int31() - (1 << 30)
			case allneg:
				res[i] = -int32(rng.Intn(1 << 20))
			}
		}
		// Seed a couple of extreme values so the most-negative/most-positive
		// zigzag-extreme path in maxU is always covered.
		if len(res) > 4 {
			res[1] = -2147483648 // MinInt32
			res[2] = 2147483647  // MaxInt32
		}

		ncols := ncolsFor(res)
		pmax := feasiblePmax(tc.blockSize, tc.predOrder, tc.maxPO)
		P := 1 << pmax

		var got, want Scratch
		got.ensure(P, ncols)
		want.ensure(P, ncols)
		clear(got.sums[:P*ncols])
		clear(want.sums[:P*ncols])

		buildFinestTablesInt32(&got, res, tc.blockSize, tc.predOrder, pmax, ncols)
		refBuildFinestTablesInt32(&want, res, tc.blockSize, tc.predOrder, pmax, ncols)

		for i := range P * ncols {
			if got.sums[i] != want.sums[i] {
				part, col := i/ncols, i%ncols
				t.Fatalf("case %d (bs=%d po=%d pmax=%d ncols=%d): sums[part=%d,k=%d] = %d, want %d",
					ci, tc.blockSize, tc.predOrder, pmax, ncols, part, col, got.sums[i], want.sums[i])
			}
		}
		for p := range P {
			if got.maxU[p] != want.maxU[p] {
				t.Fatalf("case %d (bs=%d po=%d pmax=%d): maxU[%d] = %d, want %d",
					ci, tc.blockSize, tc.predOrder, pmax, p, got.maxU[p], want.maxU[p])
			}
		}
	}
}

// TestPartitionSumsParity checks the SIMD seam directly against the scalar
// reference over the full FLAC parameter range, including empty input and widths
// on both sides of the 15-wide kernel.
func TestPartitionSumsParity(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	lengths := []int{0, 1, 7, 8, 9, 16, 31, 256, 4096}
	widths := []int{1, 3, 14, 15, 16, 31}
	for _, n := range lengths {
		res := make([]int32, n)
		for i := range res {
			res[i] = int32(rng.Uint32()) // full int32 range incl. negatives
		}
		for _, m := range widths {
			got := make([]uint64, m)
			want := make([]uint64, m)
			for i := range got {
				got[i] = ^uint64(0) // sentinel: partitionSums must fully overwrite, incl. n==0
			}
			partitionSums(got, res)
			for _, r := range res {
				u := zigzag(r)
				for k := range want {
					want[k] += u >> (uint(k) & 63)
				}
			}
			for k := range want {
				if got[k] != want[k] {
					t.Fatalf("n=%d m=%d: sums[%d] = %d, want %d", n, m, k, got[k], want[k])
				}
			}
		}
	}
}

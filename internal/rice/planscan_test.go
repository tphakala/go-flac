package rice

import (
	"math/rand"
	"testing"
)

// refGlobalMaxU is the whole-block min/max scan PlanResidualInt32 used to run
// separately to size ncols. The split planner must derive the identical value by
// reducing the per-partition maxU (folding any residual tail beyond the
// partitioned region), so finestMaxU's return value is checked against this
// oracle. Eliminating this scan is the whole point of the optimization, so this
// test is what proves the change is byte-identical.
func refGlobalMaxU(res []int32) uint64 {
	if len(res) == 0 {
		return 0
	}
	lo, hi := res[0], res[0]
	for _, r := range res {
		if r < lo {
			lo = r
		}
		if r > hi {
			hi = r
		}
	}
	return max(zigzag(lo), zigzag(hi))
}

// refPartMaxU computes each finest partition's max zigzag the obvious way (max
// over zigzag of every element), independent of finestMaxU's lo/hi extreme trick,
// so it is a true oracle for the per-partition maxU values.
func refPartMaxU(res []int32, blockSize, predOrder, pmax int) []uint64 {
	P := 1 << pmax
	partLen := blockSize >> pmax
	out := make([]uint64, P)
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		var mu uint64
		for j := range n {
			if u := zigzag(res[idx+j]); u > mu {
				mu = u
			}
		}
		out[p] = mu
		idx += n
	}
	return out
}

// refBuildFinestSums accumulates only the partition sums (no maxU), the obvious
// scalar way, as the parity oracle for the SIMD buildFinestSumsInt32 path.
func refBuildFinestSums(sc *Scratch, res []int32, blockSize, predOrder, pmax, ncols int) {
	P := 1 << pmax
	partLen := blockSize >> pmax
	idx := 0
	for p := range P {
		n := partLen
		if p == 0 {
			n -= predOrder
		}
		row := sc.sums[p*ncols : p*ncols+ncols]
		for j := range n {
			u := zigzag(res[idx+j])
			for k := range row {
				row[k] += u >> (uint(k) & 63)
			}
		}
		idx += n
	}
}

func TestFinestMaxU(t *testing.T) {
	cases := []struct {
		blockSize, predOrder, maxPO, extra int // extra: res length beyond blockSize-predOrder
	}{
		{4096, 2, 8, 0},
		{4096, 0, 8, 0},
		{4096, 12, 6, 0},
		{4608, 2, 7, 0}, // non-power-of-two block
		{512, 1, 4, 0},  // small block
		{4096, 2, 0, 0}, // pmax forced to 0: single partition
		{4096, 2, 8, 2}, // over-length tail (no-alloc-test shape: res longer than the partitioned region)
		{2048, 3, 8, 5}, // larger over-length tail
	}
	rng := rand.New(rand.NewSource(0xC0FFEE))
	for ci, tc := range cases {
		region := tc.blockSize - tc.predOrder
		n := region + tc.extra
		res := make([]int32, n)
		if tc.extra == 0 {
			for i := range res {
				res[i] = int32(rng.Intn(1<<18) - (1 << 17))
			}
			if n > 6 {
				res[1] = -2147483648 // MinInt32, exercises the most-negative zigzag extreme
				res[3] = 2147483647  // MaxInt32
			}
		} else {
			// Partition region holds only small values; the unique global maximum
			// lives in the tail. finestMaxU must fold the tail into its returned
			// global value, so this case fails unless that fold is present.
			for i := range region {
				res[i] = int32(rng.Intn(7) - 3)
			}
			for i := region; i < n; i++ {
				res[i] = int32(rng.Intn(15) - 7)
			}
			res[n-1] = 1 << 28 // unique global max, in the tail
		}

		pmax := feasiblePmax(tc.blockSize, tc.predOrder, tc.maxPO)
		P := 1 << pmax
		var sc Scratch
		sc.ensurePlan(P)

		gotGlobal := finestMaxU(&sc, res, tc.blockSize, tc.predOrder, pmax)
		if wantGlobal := refGlobalMaxU(res); gotGlobal != wantGlobal {
			t.Fatalf("case %d (bs=%d po=%d extra=%d): finestMaxU global=%d, want whole-block scan=%d",
				ci, tc.blockSize, tc.predOrder, tc.extra, gotGlobal, wantGlobal)
		}
		wantPart := refPartMaxU(res, tc.blockSize, tc.predOrder, pmax)
		for p := range P {
			if sc.maxU[p] != wantPart[p] {
				t.Fatalf("case %d (bs=%d po=%d): maxU[%d]=%d, want %d",
					ci, tc.blockSize, tc.predOrder, p, sc.maxU[p], wantPart[p])
			}
		}
	}
}

func TestBuildFinestSumsInt32Parity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5057))
	cases := []struct{ blockSize, predOrder, maxPO int }{
		{4096, 2, 8}, {4096, 0, 8}, {4608, 2, 7}, {512, 1, 4}, {4096, 2, 0}, {2048, 3, 8},
	}
	for ci, tc := range cases {
		res := make([]int32, tc.blockSize-tc.predOrder)
		for i := range res {
			res[i] = int32(rng.Intn(8001) - 4000)
		}
		if len(res) > 4 {
			res[1] = -2147483648
			res[2] = 2147483647
		}
		ncols := ncolsFor(res)
		pmax := feasiblePmax(tc.blockSize, tc.predOrder, tc.maxPO)
		P := 1 << pmax

		var got, want Scratch
		got.ensure(P, ncols)
		want.ensure(P, ncols)
		// got is left with a non-zero sentinel: buildFinestSumsInt32 must fully
		// overwrite every column (it relies on partitionSums not pre-zeroing).
		// ensure sized both sums slices to exactly P*ncols, so no reslice is needed.
		for i := range got.sums {
			got.sums[i] = ^uint64(0)
		}
		clear(want.sums)

		buildFinestSumsInt32(&got, res, tc.blockSize, tc.predOrder, pmax, ncols)
		refBuildFinestSums(&want, res, tc.blockSize, tc.predOrder, pmax, ncols)

		for i := range P * ncols {
			if got.sums[i] != want.sums[i] {
				part, col := i/ncols, i%ncols
				t.Fatalf("case %d (bs=%d po=%d pmax=%d ncols=%d): sums[part=%d,k=%d]=%d, want %d",
					ci, tc.blockSize, tc.predOrder, pmax, ncols, part, col, got.sums[i], want.sums[i])
			}
		}
	}
}

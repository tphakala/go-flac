package i32

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// Tests for the int32-accumulating restore (LPCRestoreNarrow, lpcRestoreScalarNarrow)
// and its safety predicate. lpcRestoreGo (int64 accumulation) is the reference.

func TestLPCRestoreNarrowSafe_MatchesLibFLAC(t *testing.T) {
	neg := func(n int, v int32) []int32 {
		c := make([]int32, n)
		for i := range c {
			c[i] = v
		}
		return c
	}
	// The first 14 rows are libFLAC 1.5.0's FLAC__lpc_max_residual_bps and
	// FLAC__lpc_max_prediction_before_shift_bps function bodies evaluated for the
	// same inputs; want is "both <= 32".
	tests := []struct {
		name   string
		bps    uint
		coeffs []int32
		shift  uint
		want   bool
	}{
		{"abs 65535", 16, []int32{32768, 32767}, 15, true},
		{"abs 65536", 16, []int32{32768, -32768}, 15, false},
		{"abs 65535 shift 0, second condition fails", 16, []int32{32768, 32767}, 0, false},
		{"abs 65534 shift 0", 16, []int32{32767, 32767}, 0, true},
		{"side-like 25 bps abs 127", 25, []int32{127}, 12, true},
		{"25 bps abs 128", 25, []int32{-128}, 12, false},
		{"24 bps abs 127 shift 0", 24, []int32{127}, 0, true},
		{"31 bps abs 1 shift 0", 31, []int32{1}, 0, false},
		{"31 bps abs 1 shift 1", 31, []int32{1}, 1, true},
		{"32 bps abs 0", 32, []int32{0}, 5, false},
		{"typical 16-bit", 16, []int32{8191, -4096, 2048, -1}, 12, true},
		{"17-bit side abs 32768", 17, []int32{10923, -10923, 10922}, 13, false},
		{"17-bit side abs 32767", 17, []int32{10923, -10923, 10921}, 13, true},
		{"typical 24-bit", 24, []int32{8191, -4096, 2048, -1}, 12, false},
		// Edge cases of the Go implementation.
		{"MinInt32 coefficient at 1 bps", 1, []int32{math.MinInt32}, 0, false},
		{"MinInt32 coefficient at 16 bps", 16, []int32{math.MinInt32}, 31, false},
		{"MaxInt32 coefficient at 1 bps", 1, []int32{math.MaxInt32}, 0, false},
		// A = 32 * 2^14 = 2^19: condition 1 holds for bps <= 12 only.
		{"32 x -(1<<14) at 12 bps", 12, neg(32, -(1 << 14)), 0, true},
		{"32 x -(1<<14) at 13 bps", 13, neg(32, -(1 << 14)), 0, false},
		{"32 x -(1<<14) at 1 bps", 1, neg(32, -(1 << 14)), 0, true},
		{"32 x -(1<<14) at 32 bps", 32, neg(32, -(1 << 14)), 0, false},
		{"no coefficients at 16 bps", 16, nil, 0, true},
		{"no coefficients at 32 bps", 32, nil, 0, false},
		{"bps 0", 0, []int32{1}, 0, false},
		{"bps 33", 33, []int32{1}, 0, false},
		{"shift 31", 8, []int32{1}, 31, true},
		{"shift 32", 8, []int32{1}, 32, false},
		{"order 33", 8, neg(33, 1), 0, false},
		{"order 32", 8, neg(32, 1), 0, true},
	}
	for _, tc := range tests {
		if got := LPCRestoreNarrowSafe(tc.coeffs, tc.bps, tc.shift); got != tc.want {
			t.Errorf("%s: LPCRestoreNarrowSafe(bps=%d, shift=%d) = %v, want %v", tc.name, tc.bps, tc.shift, got, tc.want)
		}
	}
}

// narrowEdgeBps and narrowEdgeOrders are the sample widths and orders the
// predicate-edge tests cover.
var (
	narrowEdgeBps    = []uint{8, 15, 16, 17, 20, 24, 25}
	narrowEdgeOrders = []int{1, 2, 4, 7, 8, 12, 13, 32}
)

// splitNeg returns order negative coefficients whose absolute values sum to a.
func splitNeg(order int, a int64) []int32 {
	c := make([]int32, order)
	base, rem := a/int64(order), a%int64(order)
	for j := range c {
		v := base
		if int64(j) < rem {
			v++
		}
		c[j] = int32(-v)
	}
	return c
}

// constantSignal is n samples of v.
func constantSignal(n int, v int32) []int32 {
	s := make([]int32, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// narrowKernelsFor returns every narrow restore this build has for order:
// the public entry and, when a specialized kernel exists, the direct one.
func narrowKernelsFor(order int) map[string]func(out, res, coeffs []int32, shift uint) {
	k := map[string]func(out, res, coeffs []int32, shift uint){"LPCRestoreNarrow": LPCRestoreNarrow}
	if order <= maxScalarKernelOrderNarrow {
		k["lpcRestoreScalarNarrow"] = lpcRestoreScalarNarrow
	}
	return k
}

// narrowEdgeSignal returns the worst-case signal for coefficient sum a: every
// sample at -2^(bps-1) and every coefficient negative, so the unshifted
// prediction is a * 2^(bps-1). res is its int64-path encode at shift 1.
func narrowEdgeSignal(bps uint, order int, a int64) (samples, res, coeffs []int32) {
	n := order + 8
	samples = constantSignal(n, int32(-1)<<(bps-1))
	coeffs = splitNeg(order, a)
	res = make([]int32, n)
	lpcResidualEncodeGo(res, samples, coeffs, narrowEdgeShift)
	return samples, res, coeffs
}

// narrowEdgeShift is 1, which keeps the second predicate condition true at the
// largest sum the first one admits.
const narrowEdgeShift = 1

// TestLPCRestoreNarrow_EdgeOfPredicate restores the worst-case signal at the
// largest coefficient sum the predicate admits, where the int32 sum is
// 2^31 - 2^(bps-1), and one past it, where the sum is exactly 2^31 and wraps in
// an int32 accumulator (so it also pins which orders LPCRestoreNarrow routes to
// the narrow kernels).
func TestLPCRestoreNarrow_EdgeOfPredicate(t *testing.T) {
	for _, bps := range narrowEdgeBps {
		for _, order := range narrowEdgeOrders {
			samples, res, coeffs := narrowEdgeSignal(bps, order, int64(1)<<(32-bps)-1)
			if !LPCRestoreNarrowSafe(coeffs, bps, narrowEdgeShift) {
				t.Fatalf("bps=%d order=%d: predicate is false at the largest passing sum", bps, order)
			}
			want := make([]int32, len(res))
			lpcRestoreGo(want, res, coeffs, narrowEdgeShift)
			if !slices.Equal(want, samples) {
				t.Fatalf("bps=%d order=%d: int64 reference does not invert the encode", bps, order)
			}
			for name, f := range narrowKernelsFor(order) {
				got := make([]int32, len(res))
				f(got, res, coeffs, narrowEdgeShift)
				equalI32(t, name+" at the edge bps="+itoa(int(bps))+" order="+itoa(order), got, samples)
			}

			// One past the edge: the direct narrow kernel wraps; the public entry
			// wraps only where it routes to a narrow kernel.
			samples, res, coeffs = narrowEdgeSignal(bps, order, int64(1)<<(32-bps))
			if LPCRestoreNarrowSafe(coeffs, bps, narrowEdgeShift) {
				t.Fatalf("bps=%d order=%d: predicate is true one past the edge", bps, order)
			}
			if order <= maxScalarKernelOrderNarrow {
				got := make([]int32, len(res))
				lpcRestoreScalarNarrow(got, res, coeffs, narrowEdgeShift)
				if got[order] == samples[order] {
					t.Errorf("bps=%d order=%d: narrow kernel did not wrap past the edge (got[%d]=%d)", bps, order, order, got[order])
				}
			}
			// The public entry wraps exactly for the orders it routes to the
			// narrow kernels and matches the int64 route above them. The
			// ceiling is a per-architecture literal (wantMaxScalarRestoreOrderNarrow,
			// pinned in the arch test files), and the orders 7, 8, 12 and 13
			// straddle the ceilings.
			got := make([]int32, len(res))
			LPCRestoreNarrow(got, res, coeffs, narrowEdgeShift)
			if narrow := order <= wantMaxScalarRestoreOrderNarrow; narrow == (got[order] == samples[order]) {
				t.Errorf("bps=%d order=%d: LPCRestoreNarrow routed narrow=%v but got[%d]=%d, sample %d",
					bps, order, narrow, order, got[order], samples[order])
			}
		}
	}
}

// narrowCase is one randomly generated predicate-satisfying subframe.
type narrowCase struct {
	bps     uint
	samples []int32
	coeffs  []int32
	shift   uint
	res     []int32
}

// randomNarrowCase draws samples in the bps-bit range (5% at the extremes) and
// coefficients whose absolute sum is at most 2^(32-bps) - 2 (which satisfies both
// conditions for every shift) and, half of the time, exactly that. The residual
// is the int64 encode of the samples.
func randomNarrowCase(r *rand.Rand, bps uint, order, n int, shift uint) narrowCase {
	limit := int64(1)<<(32-bps) - 2
	coeffs := make([]int32, order)
	var a int64
	for j := range coeffs {
		m := r.Int64N(limit/int64(order) + 1)
		a += m
		if r.IntN(2) == 0 {
			m = -m
		}
		coeffs[j] = int32(m)
	}
	if r.IntN(2) == 0 {
		// Top up one coefficient so A == limit, keeping its sign.
		j := r.IntN(order)
		extra := int32(limit - a)
		if coeffs[j] < 0 {
			extra = -extra
		}
		coeffs[j] += extra
	}
	lo, hi := -(int32(1) << (bps - 1)), int32(1)<<(bps-1)-1
	samples := make([]int32, n)
	for i := range samples {
		switch r.IntN(20) {
		case 0:
			samples[i] = lo
		case 1:
			samples[i] = hi
		default:
			samples[i] = lo + int32(r.Int64N(int64(hi)-int64(lo)+1))
		}
	}
	res := make([]int32, n)
	lpcResidualEncodeGo(res, samples, coeffs, shift)
	return narrowCase{bps: bps, samples: samples, coeffs: coeffs, shift: shift, res: res}
}

var narrowTestBps = []uint{8, 12, 16, 17, 20, 24, 25}

var narrowTestShifts = []uint{0, 1, 2, 5, 9, 12, 14, 15, 31}

// forEachNarrowCase runs f over orders 1..maxLPCRestoreOrder, the lpcSizes block
// sizes and every shift in narrowTestShifts, asserting the generated case passes
// the predicate (so the tests cannot cover only rejected inputs).
func forEachNarrowCase(t *testing.T, f func(c narrowCase, order int)) {
	t.Helper()
	r := rand.New(rand.NewPCG(7, 11))
	for order := 1; order <= maxLPCRestoreOrder; order++ {
		for _, n := range lpcSizes {
			for _, shift := range narrowTestShifts {
				bps := narrowTestBps[r.IntN(len(narrowTestBps))]
				c := randomNarrowCase(r, bps, order, n, shift)
				if !LPCRestoreNarrowSafe(c.coeffs, c.bps, c.shift) {
					t.Fatalf("generated case rejected by the predicate: bps=%d order=%d shift=%d coeffs=%v", c.bps, order, shift, c.coeffs)
				}
				f(c, order)
			}
		}
	}
}

func TestLPCRestoreNarrow_MatchesGo(t *testing.T) {
	forEachNarrowCase(t, func(c narrowCase, order int) {
		n := len(c.res)
		want := make([]int32, n)
		lpcRestoreGo(want, c.res, c.coeffs, c.shift)
		label := func() string {
			return "LPCRestoreNarrow bps=" + itoa(int(c.bps)) + " order=" + itoa(order) + " n=" + itoa(n) + " shift=" + itoa(int(c.shift))
		}
		sameI32(t, want, c.samples, label)
		got := make([]int32, n)
		LPCRestoreNarrow(got, c.res, c.coeffs, c.shift)
		sameI32(t, got, want, label)
	})
}

func TestLPCRestoreScalarNarrow_MatchesGo(t *testing.T) {
	forEachNarrowCase(t, func(c narrowCase, order int) {
		if order > maxScalarKernelOrderNarrow {
			return
		}
		n := len(c.res)
		label := func() string {
			return "lpcRestoreScalarNarrow bps=" + itoa(int(c.bps)) + " order=" + itoa(order) + " n=" + itoa(n) + " shift=" + itoa(int(c.shift))
		}
		got := make([]int32, n)
		lpcRestoreScalarNarrow(got, c.res, c.coeffs, c.shift)
		sameI32(t, got, c.samples, label)

		inPlace := slices.Clone(c.res)
		lpcRestoreScalarNarrow(inPlace, inPlace, c.coeffs, c.shift)
		sameI32(t, inPlace, c.samples, func() string { return "in-place " + label() })
	})
}

func TestLPCRestoreNarrow_InPlace(t *testing.T) {
	forEachNarrowCase(t, func(c narrowCase, order int) {
		got := slices.Clone(c.res)
		LPCRestoreNarrow(got, got, c.coeffs, c.shift) // dst aliases residual exactly
		sameI32(t, got, c.samples, func() string {
			return "in-place LPCRestoreNarrow bps=" + itoa(int(c.bps)) + " order=" + itoa(order) + " n=" + itoa(len(c.res)) + " shift=" + itoa(int(c.shift))
		})
	})
}

// narrowProbeOrders returns the highest narrow-routed order, the first order
// above it (which runs LPCRestore) and 32.
func narrowProbeOrders() []int {
	return []int{maxScalarRestoreOrderNarrow, maxScalarRestoreOrderNarrow + 1, maxLPCRestoreOrder}
}

func TestLPCRestoreNarrow_NoOverwrite(t *testing.T) {
	const tail = 4
	r := rand.New(rand.NewPCG(3, 5))
	for _, order := range narrowProbeOrders() {
		for _, n := range []int{order + 1, order + 7, 100} {
			c := randomNarrowCase(r, 16, order, n, 12)
			res := append(slices.Clone(c.res), make([]int32, tail)...)
			resBefore := slices.Clone(res)
			buf := make([]int32, n+tail)
			for i := n; i < len(buf); i++ {
				buf[i] = math.MaxInt32
			}
			LPCRestoreNarrow(buf[:n], res, c.coeffs, 12)
			for i := n; i < len(buf); i++ {
				if buf[i] != math.MaxInt32 {
					t.Fatalf("order=%d n=%d: out[%d] past the slice was overwritten", order, n, i)
				}
			}
			if !slices.Equal(res, resBefore) {
				t.Fatalf("order=%d n=%d: residual was modified in the two-buffer call", order, n)
			}
			sameI32(t, buf[:n], c.samples, func() string { return "order=" + itoa(order) + " n=" + itoa(n) })
		}
	}
}

func TestLPCRestoreNarrow_AllocFree(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 13))
	for _, order := range narrowProbeOrders() {
		c := randomNarrowCase(r, 16, order, 1024, 12)
		out := make([]int32, 1024)
		if got := testing.AllocsPerRun(50, func() { LPCRestoreNarrow(out, c.res, c.coeffs, 12) }); got != 0 {
			t.Errorf("LPCRestoreNarrow order=%d allocated %v times per run, want 0", order, got)
		}
		if got := testing.AllocsPerRun(50, func() { _ = LPCRestoreNarrowSafe(c.coeffs, 16, 12) }); got != 0 {
			t.Errorf("LPCRestoreNarrowSafe order=%d allocated %v times per run, want 0", order, got)
		}
	}
}

// TestLPCRestoreNarrow_NoPanicOnHostileInput feeds full-range coefficients and
// residuals, for which the predicate does not hold, and checks only that the
// result has the right length and nothing panics.
func TestLPCRestoreNarrow_NoPanicOnHostileInput(t *testing.T) {
	r := rand.New(rand.NewPCG(21, 22))
	for range 500 {
		order := r.IntN(maxLPCRestoreOrder + 2)
		n := r.IntN(200)
		coeffs := make([]int32, order)
		for j := range coeffs {
			coeffs[j] = int32(r.Uint32())
		}
		res := make([]int32, n)
		for i := range res {
			res[i] = int32(r.Uint32())
		}
		shift := uint(r.IntN(80))
		out := make([]int32, n)
		LPCRestoreNarrow(out, res, coeffs, shift)
	}
}

// TestLPCRestoreScalarNarrow_FallsBackOutsideKernelRange pins that orders without a
// narrow kernel (0 and above maxScalarKernelOrderNarrow) restore like LPCRestore
// when lpcRestoreScalarNarrow is called directly.
func TestLPCRestoreScalarNarrow_FallsBackOutsideKernelRange(t *testing.T) {
	r := rand.New(rand.NewPCG(31, 37))
	for _, order := range []int{0, maxScalarKernelOrderNarrow + 1, maxLPCRestoreOrder} {
		c := randomNarrowCase(r, 16, max(order, 1), 200, 12)
		coeffs := c.coeffs[:order]
		want := make([]int32, len(c.res))
		LPCRestore(want, c.res, coeffs, 12)
		got := make([]int32, len(c.res))
		lpcRestoreScalarNarrow(got, c.res, coeffs, 12)
		equalI32(t, "lpcRestoreScalarNarrow order="+itoa(order), got, want)
	}
}

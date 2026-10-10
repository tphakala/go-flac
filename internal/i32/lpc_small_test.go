package i32

import (
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

// Tests for the order-specialized scalar restore kernels (lpcRestoreScalar).
// lpcRestoreGo is the bit-exact reference for the full int32 coefficient and
// shift domain; lpcRestoreOracle (big.Int) is the independent reference where
// the int64 sum cannot disagree with exact arithmetic.

// Oracle shifts. The oracle takes bits s..s+31 of the exact sum and the int64
// sum holds bits 0..63 of it, so they agree whenever s+31 <= 63 even after
// wraparound. Families whose sums cannot overflow int64 are valid at any shift.
var (
	oracleShiftsExact   = []uint{0, 1, 9, 14, 15, 31, 32, 33, 63}
	oracleShiftsWrapped = []uint{0, 1, 14, 31, 32}
)

// smallShifts covers every in-range shift plus counts past the clamp.
var smallShifts = func() []uint {
	shifts := make([]uint, 0, 68)
	for s := range uint(64) {
		shifts = append(shifts, s)
	}
	return append(shifts, 64, 65, 100, 1<<20)
}()

// sameI32 is equalI32 with the label built only on mismatch, so the hot loops
// do not allocate a string per comparison.
func sameI32(t *testing.T, got, want []int32, label func() string) {
	t.Helper()
	if !slices.Equal(got, want) {
		equalI32(t, label(), got, want)
	}
}

// largeShifts is the reduced shift set for the long inputs, where the full set
// would dominate the test time without exercising more kernel paths.
var largeShifts = []uint{0, 1, 14, 15, 31, 32, 33, 63, 64, 1 << 20}

func fillCoeffs(order int, f func(j int) int32) []int32 {
	c := make([]int32, order)
	for j := range c {
		c[j] = f(j)
	}
	return c
}

type smallCoeffFamily struct {
	name         string
	coeffs       func(order int) []int32
	oracleShifts []uint
}

var smallCoeffFamilies = []smallCoeffFamily{
	{"qlp", func(order int) []int32 {
		return fillCoeffs(order, func(j int) int32 {
			v := int32(12000 >> j)
			if j%2 == 1 {
				v = -v
			}
			return v
		})
	}, oracleShiftsExact},
	{"maxInt32", func(order int) []int32 {
		return fillCoeffs(order, func(int) int32 { return math.MaxInt32 })
	}, oracleShiftsWrapped},
	{"minInt32", func(order int) []int32 {
		return fillCoeffs(order, func(int) int32 { return math.MinInt32 })
	}, oracleShiftsWrapped},
	{"altExtreme", func(order int) []int32 {
		return fillCoeffs(order, func(j int) int32 {
			if j%2 == 0 {
				return math.MaxInt32
			}
			return math.MinInt32
		})
	}, oracleShiftsWrapped},
}

type smallResFamily struct {
	name string
	fill func(res []int32)
}

func constFill(v int32) func([]int32) {
	return func(res []int32) {
		for i := range res {
			res[i] = v
		}
	}
}

var smallResFamilies = []smallResFamily{
	{"extremes", fillLPCSamples},
	{"maxInt32", constFill(math.MaxInt32)},
	{"minInt32", constFill(math.MinInt32)},
	{"small", func(res []int32) {
		for i := range res {
			res[i] = int32(i%17) - 8
		}
	}},
	{"zeros", constFill(0)},
}

func smallSizes(order int) []int {
	sizes := make([]int, 0, order+8)
	sizes = append(sizes, 16, 33, 64, 1000, 1025)
	for n := 0; n <= order+2; n++ {
		sizes = append(sizes, n)
	}
	return sizes
}

// smallCase is one (coefficients, residual, size) combination.
type smallCase struct {
	name         string
	coeffs, res  []int32
	oracleShifts []uint
}

// smallCasesByOrder holds, for each order 0..maxScalarKernelOrder+1, every coefficient family x
// residual family x size plus the unit-tap vectors (a mis-rotated history or
// swapped coefficient changes the output). Built once and shared by the tests.
var smallCasesByOrder = sync.OnceValue(func() [][]smallCase {
	byOrder := make([][]smallCase, maxScalarKernelOrder+2)
	for order := range byOrder {
		type resKey struct {
			family string
			n      int
		}
		resCache := map[resKey][]int32{}
		add := func(cname string, coeffs []int32, shifts []uint) {
			for _, rf := range smallResFamilies {
				for _, n := range smallSizes(order) {
					k := resKey{rf.name, n}
					res, ok := resCache[k]
					if !ok {
						res = make([]int32, n)
						rf.fill(res)
						resCache[k] = res
					}
					byOrder[order] = append(byOrder[order], smallCase{
						name:         cname + "/" + rf.name,
						coeffs:       coeffs,
						res:          res,
						oracleShifts: shifts,
					})
				}
			}
		}
		for _, cf := range smallCoeffFamilies {
			add(cf.name, cf.coeffs(order), cf.oracleShifts)
		}
		for j := range order {
			c := make([]int32, order)
			c[j] = 1
			add("unit"+itoa(j), c, oracleShiftsExact)
		}
	}
	return byOrder
})

// TestLPCRestoreSmallOrder_MatchesGo checks every order 0..maxScalarKernelOrder+1 against the
// reference for all shifts, including exact in-place aliasing (orders 1..maxScalarKernelOrder
// run the specialized kernels; orders outside that range fall back to the
// reference path inside lpcRestoreScalar and must agree trivially).
func TestLPCRestoreSmallOrder_MatchesGo(t *testing.T) {
	for order, cases := range smallCasesByOrder() {
		for _, tc := range cases {
			n := len(tc.res)
			got := make([]int32, n)
			want := make([]int32, n)
			inPlace := make([]int32, n)
			shifts := smallShifts
			if n > 64 {
				shifts = largeShifts
			}
			for _, shift := range shifts {
				lpcRestoreScalar(got, tc.res, tc.coeffs, shift)
				lpcRestoreGo(want, tc.res, tc.coeffs, shift)
				sameI32(t, got, want, func() string { return "scalar order=" + itoa(order) + " " + tc.name + " shift=" + itoa(int(shift)) })

				copy(inPlace, tc.res)
				lpcRestoreScalar(inPlace, inPlace, tc.coeffs, shift)
				sameI32(t, inPlace, want, func() string { return "in-place order=" + itoa(order) + " " + tc.name + " shift=" + itoa(int(shift)) })
			}
		}
	}
}

func TestLPCRestoreSmallOrder_MatchesOracle(t *testing.T) {
	for order := 1; order <= maxScalarKernelOrder; order++ {
		for _, tc := range smallCasesByOrder()[order] {
			n := len(tc.res)
			if n > 64 {
				continue
			}
			got := make([]int32, n)
			want := make([]int32, n)
			for _, shift := range tc.oracleShifts {
				lpcRestoreScalar(got, tc.res, tc.coeffs, shift)
				lpcRestoreOracle(want, tc.res, tc.coeffs, shift)
				sameI32(t, got, want, func() string { return "oracle order=" + itoa(order) + " " + tc.name + " shift=" + itoa(int(shift)) })
			}
		}
	}
}

func TestLPCRestoreSmallOrder_NoOverwrite(t *testing.T) {
	const tail = 4
	for order := 1; order <= maxScalarKernelOrder; order++ {
		coeffs := smallCoeffFamilies[0].coeffs(order)
		for _, n := range smallSizes(order) {
			if n > 64 {
				continue
			}
			res := make([]int32, n+tail)
			fillLPCSamples(res)
			resBefore := slices.Clone(res)
			buf := make([]int32, n+tail)
			for i := n; i < len(buf); i++ {
				buf[i] = math.MaxInt32
			}
			lpcRestoreScalar(buf[:n], res, coeffs, 12)
			for i := n; i < len(buf); i++ {
				if buf[i] != math.MaxInt32 {
					t.Fatalf("order=%d n=%d: out[%d] past the slice was overwritten", order, n, i)
				}
			}
			if !slices.Equal(res, resBefore) {
				t.Fatalf("order=%d n=%d: residual was modified in the two-buffer call", order, n)
			}
		}
	}
}

func TestLPCRestoreSmallOrder_Random(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		order := r.IntN(maxScalarKernelOrder + 2)
		n := r.IntN(301)
		coeffs := make([]int32, order)
		full := r.IntN(2) == 0
		for j := range coeffs {
			if full {
				coeffs[j] = int32(r.Uint32())
			} else {
				coeffs[j] = int32(r.IntN(1<<15)) - 1<<14
			}
		}
		res := make([]int32, n)
		for i := range res {
			res[i] = int32(r.Uint32())
		}
		shift := uint(r.IntN(71))

		want := make([]int32, n)
		lpcRestoreGo(want, res, coeffs, shift)

		got := make([]int32, n)
		lpcRestoreScalar(got, res, coeffs, shift)
		sameI32(t, got, want, func() string {
			return "lpcRestoreScalar order=" + itoa(order) + " n=" + itoa(n) + " shift=" + itoa(int(shift))
		})

		// Public entry clamps the shift and routes through the dispatcher.
		LPCRestore(got, res, coeffs, shift)
		sameI32(t, got, want, func() string {
			return "LPCRestore order=" + itoa(order) + " n=" + itoa(n) + " shift=" + itoa(int(shift))
		})
	}
}

// TestLPCRestoreDispatch_ParityWithGo is untagged and goes through the public
// LPCRestore, so it covers the per-architecture scalar routing and lpcRestoreI32 on
// every build variant (amd64, arm64 and the generic fallback). Under
// GODEBUG=cpu.avx2=off it also covers lpcRestoreGo above the scalar ceiling.
func TestLPCRestoreDispatch_ParityWithGo(t *testing.T) {
	for order := 1; order <= maxLPCRestoreOrder; order++ {
		coeffs := smallCoeffFamilies[0].coeffs(order)
		for _, n := range lpcSizes {
			res := make([]int32, n)
			fillLPCSamples(res)
			got := make([]int32, n)
			want := make([]int32, n)
			for _, shift := range lpcShifts {
				LPCRestore(got, res, coeffs, shift)
				lpcRestoreGo(want, res, coeffs, shift)
				sameI32(t, got, want, func() string {
					return "LPCRestore order=" + itoa(order) + " n=" + itoa(n) + " shift=" + itoa(int(shift))
				})
			}
		}
	}
}

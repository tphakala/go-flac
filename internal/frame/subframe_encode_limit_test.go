package frame

import (
	"math"
	"slices"
	"testing"
)

// maxResidual64 bounds the residual magnitude RFC 9639 section 9.2.7.3 allows:
// an absolute value of 2^31 or more is illegal, so -2^31 is out too.
const maxResidual64 = math.MaxInt32

// stepSignal returns n samples that sit at lo for the first half and at hi for
// the second, so a first-order predictor sees one residual of hi-lo.
func stepSignal(n int, lo, hi int64) []int64 {
	s := make([]int64, n)
	for i := range s {
		if i < n/2 {
			s[i] = lo
		} else {
			s[i] = hi
		}
	}
	return s
}

// TestPlanSubframe64ResidualLimit pins that planSubframe64 never plans a FIXED or
// LPC subframe whose residuals fall outside [-(2^31-1), 2^31-1], and falls back to
// verbatim when the cheapest candidate would. At level 3 the sine row with a
// trailing spike makes an LPC subframe the cheapest candidate, so it covers the
// LPC branch as well as the FIXED one.
func TestPlanSubframe64ResidualLimit(t *testing.T) {
	const n = 4096
	sine := make([]int64, n)
	for i := range sine {
		sine[i] = int64(float64(1<<30)*math.Sin(float64(i)*0.05)) + int64(i%3)
	}
	spike := slices.Clone(sine)
	spike[n-2], spike[n-1] = -math.MaxInt32, math.MaxInt32
	ramp := make([]int64, n)
	for i := range ramp {
		ramp[i] = int64(i-n/2) * 1000
	}

	cases := []struct {
		name         string
		s            []int64
		bps          int
		wantVerbatim bool
	}{
		{"32-bit step", stepSignal(n, math.MinInt32, math.MaxInt32), 32, true},
		{"33-bit side step", stepSignal(n, -(1<<32 - 1), 1<<32-1), 33, true},
		{"32-bit sine with trailing spike", spike, 32, true},
		// A first-order step of exactly 2^31-1 is legal; -2^31 and 2^31 are not. One
		// sample of each step is odd so the samples have no common wasted bits, which
		// would shrink the residual before the check.
		{"32-bit step by 2^31-1", stepSignal(n, -1, math.MaxInt32-1), 32, false},
		{"32-bit step by -2^31", stepSignal(n, math.MaxInt32, -1), 32, true},
		{"33-bit step by 2^31", stepSignal(n, -1, math.MaxInt32), 33, true},
		{"32-bit ramp", ramp, 32, false},
		{"32-bit sine", sine, 32, false},
	}
	for _, tc := range cases {
		for _, level := range []int{0, 3, 8} {
			p := paramsLevel(t, level)
			var ws Workspace
			plan := planSubframe64(&ws, 0, tc.s, tc.bps, p, ws.window(p, n))
			if tc.wantVerbatim && plan.kind != 1 {
				t.Errorf("%s level %d: kind = %d, want 1 (verbatim)", tc.name, level, plan.kind)
			}
			if !tc.wantVerbatim && plan.kind == 1 {
				t.Errorf("%s level %d: fell back to verbatim, want a predictor", tc.name, level)
			}
			if plan.kind != 2 && plan.kind != 3 {
				continue
			}
			for i, r := range ws.cand[0].res64[:n-plan.order] {
				if r < -maxResidual64 || r > maxResidual64 {
					t.Errorf("%s level %d: kind %d residual[%d] = %d outside the RFC range", tc.name, level, plan.kind, i, r)
					break
				}
			}
		}
	}
}

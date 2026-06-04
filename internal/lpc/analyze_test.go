package lpc

import (
	"math"
	"testing"
)

func TestTukeyWindow(t *testing.T) {
	const n = 8
	w := TukeyWindow(n, 0.5)
	if len(w) != n {
		t.Fatalf("len = %d, want %d", len(w), n)
	}
	// All weights in [0, 1].
	for i, v := range w {
		if v < 0 || v > 1 {
			t.Fatalf("w[%d] = %v out of [0,1]", i, v)
		}
	}
	// Symmetric.
	for i := range n / 2 {
		if math.Abs(w[i]-w[n-1-i]) > 1e-12 {
			t.Fatalf("not symmetric at %d: %v vs %v", i, w[i], w[n-1-i])
		}
	}
	// Endpoints taper to ~0.
	if w[0] > 1e-9 {
		t.Fatalf("w[0] = %v, want ~0", w[0])
	}
	// Flat middle is 1 (i=3 and i=4 are inside the 50% flat region for n=8).
	if math.Abs(w[3]-1) > 1e-12 || math.Abs(w[4]-1) > 1e-12 {
		t.Fatalf("middle not flat: w[3]=%v w[4]=%v", w[3], w[4])
	}
	// Known value at i=1 (see plan derivation): 0.5*(1+cos(pi*(2/3.5 - 1))).
	want1 := 0.5 * (1 + math.Cos(math.Pi*(2.0/3.5-1)))
	if math.Abs(w[1]-want1) > 1e-12 {
		t.Fatalf("w[1] = %v, want %v", w[1], want1)
	}
}

func TestTukeyWindowEdgeCases(t *testing.T) {
	if got := TukeyWindow(1, 0.5); len(got) != 1 || got[0] != 1 {
		t.Fatalf("n=1: got %v, want [1]", got)
	}
}

func TestAutocorrelate(t *testing.T) {
	x := []float64{1, 2, 3}
	autoc := autocorrelate(x, 2)
	// autoc[0] = 1*1 + 2*2 + 3*3 = 14
	// autoc[1] = 2*1 + 3*2 = 8
	// autoc[2] = 3*1 = 3
	want := []float64{14, 8, 3}
	if len(autoc) != len(want) {
		t.Fatalf("len = %d, want %d", len(autoc), len(want))
	}
	for i := range want {
		if math.Abs(autoc[i]-want[i]) > 1e-12 {
			t.Fatalf("autoc[%d] = %v, want %v", i, autoc[i], want[i])
		}
	}
}

func TestLevinsonAR1(t *testing.T) {
	// Autocorrelation of an AR(1) process with a=0.5: R[k] = R[0]*0.5^|k|.
	autoc := []float64{1, 0.5, 0.25}
	lpcByOrder, errByOrder, maxComputed := levinson(autoc, 2)
	if maxComputed != 2 {
		t.Fatalf("maxComputed = %d, want 2", maxComputed)
	}
	// Order 1 predictor coefficient should be ~0.5 (recovers a).
	if got := lpcByOrder[1]; len(got) != 1 || math.Abs(got[0]-0.5) > 1e-9 {
		t.Fatalf("order-1 coeffs = %v, want [0.5]", got)
	}
	// Order 2 coefficients should be ~[0.5, 0] (AR(1) has no 2nd term).
	if got := lpcByOrder[2]; len(got) != 2 ||
		math.Abs(got[0]-0.5) > 1e-9 || math.Abs(got[1]-0) > 1e-9 {
		t.Fatalf("order-2 coeffs = %v, want [0.5, 0]", got)
	}
	// Prediction error must be non-increasing in order.
	if !(errByOrder[1] <= errByOrder[0]+1e-12 && errByOrder[2] <= errByOrder[1]+1e-12) {
		t.Fatalf("errors not non-increasing: %v", errByOrder)
	}
	// err[1] = err[0]*(1 - 0.5^2) = 1*0.75.
	if math.Abs(errByOrder[1]-0.75) > 1e-9 {
		t.Fatalf("err[1] = %v, want 0.75", errByOrder[1])
	}
}

func TestLevinsonStopsOnNonPositiveError(t *testing.T) {
	// Degenerate autocorrelation; recursion must not panic and must report a
	// maxComputed it can stand behind.
	autoc := []float64{0, 0, 0}
	_, _, maxComputed := levinson(autoc, 2)
	if maxComputed < 0 || maxComputed > 2 {
		t.Fatalf("maxComputed = %d out of range", maxComputed)
	}
}

func TestQuantizeCoefficientsBasic(t *testing.T) {
	qc, shift, ok := quantizeCoefficients([]float64{0.5, 0}, 15)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if shift < 0 || shift > maxQLPShift {
		t.Fatalf("shift = %d out of [0,%d]", shift, maxQLPShift)
	}
	// 0.5 with precision 15: frexp(0.5) exp=0, shift=14, qc0 = round(0.5*2^14)=8192.
	if shift != 14 || qc[0] != 8192 || qc[1] != 0 {
		t.Fatalf("qc=%v shift=%d, want qc=[8192 0] shift=14", qc, shift)
	}
}

func TestQuantizeCoefficientsRanges(t *testing.T) {
	const precision = 15
	qmax := int32(1)<<(precision-1) - 1    // 16383
	qmin := -(int32(1) << (precision - 1)) // -16384

	cases := [][]float64{
		{0.5, -0.25, 0.125},
		{0.0001},   // tiny -> shift clamps to maxQLPShift
		{20000.0},  // huge -> shift clamps to 0, coeff clamps to qmax
		{-20000.0}, // huge negative -> coeff clamps to qmin
		{1.234, -2.5, 3.75, -0.1},
	}
	for _, lpc := range cases {
		qc, shift, ok := quantizeCoefficients(lpc, precision)
		if !ok {
			t.Fatalf("ok=false for %v", lpc)
		}
		if shift < 0 || shift > maxQLPShift {
			t.Fatalf("shift %d out of range for %v", shift, lpc)
		}
		for i, q := range qc {
			if q < qmin || q > qmax {
				t.Fatalf("qc[%d]=%d out of [%d,%d] for %v", i, q, qmin, qmax, lpc)
			}
		}
	}

	// Explicit clamp checks.
	if qc, shift, _ := quantizeCoefficients([]float64{0.0001}, precision); shift != maxQLPShift {
		t.Fatalf("tiny coeff: shift=%d, want %d (qc=%v)", shift, maxQLPShift, qc)
	}
	if qc, shift, _ := quantizeCoefficients([]float64{20000.0}, precision); shift != 0 || qc[0] != qmax {
		t.Fatalf("huge coeff: shift=%d qc0=%d, want shift 0 qc0 %d", shift, qc[0], qmax)
	}
	if qc, _, _ := quantizeCoefficients([]float64{-20000.0}, precision); qc[0] != qmin {
		t.Fatalf("huge negative coeff: qc0=%d, want %d", qc[0], qmin)
	}
}

func TestQuantizeCoefficientsRejectsZero(t *testing.T) {
	if _, _, ok := quantizeCoefficients([]float64{0, 0}, 15); ok {
		t.Fatal("ok=true for all-zero coeffs, want false")
	}
}

func TestEstimateBestOrder(t *testing.T) {
	// Error drops sharply through order 2, then flattens. With a per-order
	// header cost, the estimate should settle around order 2-3, never above
	// maxComputed.
	errByOrder := []float64{100, 50, 10, 9.9, 9.8}
	const blockLen = 1000
	const perOrderHeader = 16 + 15 // eff + precision
	got := estimateBestOrder(errByOrder, 4, blockLen, perOrderHeader)
	if got < 2 || got > 3 {
		t.Fatalf("order = %d, want 2 or 3", got)
	}
	if got > 4 {
		t.Fatalf("order %d exceeds maxComputed 4", got)
	}
}

func TestEstimateBestOrderFlatPicksLow(t *testing.T) {
	// No improvement past order 1: the header cost should keep the order at 1.
	errByOrder := []float64{100, 100, 100, 100}
	got := estimateBestOrder(errByOrder, 3, 1000, 31)
	if got != 1 {
		t.Fatalf("order = %d, want 1", got)
	}
}

func TestEstimateBestOrderPerfectPrediction(t *testing.T) {
	// A non-positive error means perfect prediction at that order.
	errByOrder := []float64{100, 50, 0, 0}
	got := estimateBestOrder(errByOrder, 3, 1000, 31)
	if got != 2 {
		t.Fatalf("order = %d, want 2 (first perfect order)", got)
	}
}

// arSignal generates a deterministic 2nd-order autoregressive int32 signal that
// LPC should model well.
func arSignal(n int) []int32 {
	out := make([]int32, n)
	var p1, p2 float64
	var state uint32 = 0x2468ace0
	for i := range out {
		state = state*1664525 + 1013904223
		noise := (float64(int32(state>>9)%2001) - 1000) * 0.5
		v := 1.4*p1 - 0.6*p2 + noise
		out[i] = int32(math.Round(v))
		p2 = p1
		p1 = v
	}
	return out
}

func TestAnalyzeLPCModelsCorrelatedSignal(t *testing.T) {
	src := arSignal(4096)
	window := TukeyWindow(len(src), 0.5)

	sc := NewScratch(len(src), 12)
	var qbuf [32]int32
	order, shift, qn, ok := AnalyzeLPC(src, window, 12, 15, 16, sc, qbuf[:])
	qc := qbuf[:qn]
	if !ok {
		t.Fatal("ok = false, want true for a correlated signal")
	}
	if order < 1 || order > 12 {
		t.Fatalf("order = %d out of [1,12]", order)
	}
	if shift < 0 || shift > maxQLPShift {
		t.Fatalf("shift = %d out of range", shift)
	}
	if len(qc) != order {
		t.Fatalf("len(qc) = %d, want %d", len(qc), order)
	}

	// Residuals must invert RestoreLPC exactly (the correctness anchor).
	res := make([]int32, len(src)-order)
	ComputeLPCResiduals(res, src, qc, shift, order)
	dst := make([]int32, len(src))
	copy(dst[:order], src[:order])
	copy(dst[order:], res)
	RestoreLPC(dst, qc, shift, order)
	for i := range src {
		if dst[i] != src[i] {
			t.Fatalf("round-trip mismatch at %d: %d vs %d", i, dst[i], src[i])
		}
	}

	// The predictor must beat order-0 (raw) coding: sum|res| < sum|src-mean|-ish.
	var sumRes, sumRaw int64
	for _, r := range res {
		sumRes += abs64(int64(r))
	}
	for i := 1; i < len(src); i++ {
		sumRaw += abs64(int64(src[i]) - int64(src[i-1]))
	}
	if sumRes >= sumRaw {
		t.Fatalf("LPC did not improve over first-difference: sumRes=%d sumRaw=%d", sumRes, sumRaw)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestAnalyzeLPCShortBlock(t *testing.T) {
	sc := NewScratch(1, 8)
	var qbuf [32]int32
	if _, _, _, ok := AnalyzeLPC([]int32{5}, []float64{1}, 8, 15, 16, sc, qbuf[:]); ok {
		t.Fatal("ok=true for 1-sample block, want false")
	}
}

func TestAnalyzeLPCSilence(t *testing.T) {
	src := make([]int32, 256) // all zero -> autoc[0] == 0
	window := TukeyWindow(len(src), 0.5)
	sc := NewScratch(len(src), 8)
	var qbuf [32]int32
	if _, _, _, ok := AnalyzeLPC(src, window, 8, 15, 16, sc, qbuf[:]); ok {
		t.Fatal("ok=true for silence, want false")
	}
}

func TestAnalyzeLPCInvalidPrecision(t *testing.T) {
	src := arSignal(256)
	window := TukeyWindow(len(src), 0.5)
	sc := NewScratch(len(src), 8)
	var qbuf [32]int32
	// precision 0 would hit 1<<(precision-1); precision 16 would emit the
	// reserved 4-bit code 15. Both must cleanly skip LPC, not panic or encode
	// an invalid subframe.
	for _, prec := range []int{0, -1, 16, 32} {
		if _, _, _, ok := AnalyzeLPC(src, window, 8, prec, 16, sc, qbuf[:]); ok {
			t.Fatalf("ok=true for precision %d, want false", prec)
		}
	}
	// The supported precision (15) still works on the same signal.
	if _, _, _, ok := AnalyzeLPC(src, window, 8, 15, 16, sc, qbuf[:]); !ok {
		t.Fatal("ok=false for precision 15 on a correlated signal, want true")
	}
}

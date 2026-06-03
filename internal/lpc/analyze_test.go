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

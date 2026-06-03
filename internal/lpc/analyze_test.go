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

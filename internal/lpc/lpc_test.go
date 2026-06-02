package lpc

import "testing"

func TestRestoreFixedOrder2(t *testing.T) {
	// Encode a known ramp: samples 0,1,2,3,4. Order-2 fixed predictor residual is
	// s[n] - 2 s[n-1] + s[n-2] = 0 for a linear ramp. So residuals are all 0.
	dst := []int32{0, 1, 0, 0, 0} // warmup [0,1], residuals [0,0,0]
	RestoreFixed(dst, 2)
	want := []int32{0, 1, 2, 3, 4}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], want[i])
		}
	}
}

func TestRestoreFixedOrder0(t *testing.T) {
	dst := []int32{5, -3, 7}
	RestoreFixed(dst, 0) // order 0: samples are the residuals unchanged
	want := []int32{5, -3, 7}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], want[i])
		}
	}
}

func TestRestoreLPC(t *testing.T) {
	// Predictor: order 1, coeff[0]=2, shift=1 -> pred = (2*prev) >> 1 = prev.
	// warmup [10], residuals [1, -1, 0] -> samples 10, 11, 10, 10.
	dst := []int32{10, 1, -1, 0}
	RestoreLPC(dst, []int32{2}, 1, 1)
	want := []int32{10, 11, 10, 10}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], want[i])
		}
	}
}

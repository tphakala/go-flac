package lpc

import "testing"

// Residuals computed in int64 must invert exactly under RestoreLPC[int64].
func TestComputeLPCResiduals64InvertsRestore(t *testing.T) {
	const order = 4
	src := make([]int64, 64)
	for i := range src {
		src[i] = int64(i*i*7-13*i) << 8 // wide-ish values
	}
	src[40] = (1 << 33) - 1 // force a 33-bit sample
	qcoeff := []int32{7000, -3000, 1500, -500}
	const shift = 12

	res := make([]int64, len(src)-order)
	ComputeLPCResiduals64(res, src, qcoeff, shift, order)

	rec := make([]int64, len(src))
	copy(rec, src[:order])
	copy(rec[order:], res)
	RestoreLPC(rec, qcoeff, shift, order)
	for i := range src {
		if rec[i] != src[i] {
			t.Fatalf("LPC sample[%d] = %d, want %d", i, rec[i], src[i])
		}
	}
}

func TestComputeFixedResiduals64InvertsRestore(t *testing.T) {
	for order := 0; order <= 4; order++ {
		src := make([]int64, 48)
		for i := range src {
			src[i] = int64(i*123456789 - 1<<30)
		}
		src[20] = -(1 << 33)
		res := make([]int64, len(src)-order)
		ComputeFixedResiduals64(res, src, order)
		rec := make([]int64, len(src))
		copy(rec, src[:order])
		copy(rec[order:], res)
		RestoreFixed(rec, order)
		for i := range src {
			if rec[i] != src[i] {
				t.Fatalf("order %d sample[%d] = %d, want %d", order, i, rec[i], src[i])
			}
		}
	}
}

func TestBestFixedOrder64Runs(t *testing.T) {
	src := make([]int64, 64)
	for i := range src {
		src[i] = int64(i) << 25 // smooth ramp favors a higher fixed order
	}
	if o := BestFixedOrder64(src, 4); o < 0 || o > 4 {
		t.Fatalf("BestFixedOrder64 = %d, want 0..4", o)
	}
}

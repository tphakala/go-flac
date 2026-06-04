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

func TestFixedAbsSums64MatchesPerOrder(t *testing.T) {
	x := make([]int64, 1024)
	s := uint32(7)
	for i := range x {
		s = s*1103515245 + 12345
		x[i] = int64(int32(s>>16) % 4096)
	}
	var got [5]uint64
	FixedAbsSums64(x, &got)
	for order := range 5 {
		res := make([]int64, len(x)-order)
		ComputeFixedResiduals64(res, x, order)
		var want uint64
		for _, v := range res {
			want += absU64(v)
		}
		if got[order] != want {
			t.Fatalf("order %d: FixedAbsSums64=%d want %d", order, got[order], want)
		}
	}
}

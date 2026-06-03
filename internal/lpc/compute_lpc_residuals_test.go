package lpc

import "testing"

// genSamples returns deterministic pseudo-random int32 samples within a given
// absolute bound, using a simple LCG so the test is reproducible.
func genSamples(n int, bound int32) []int32 {
	out := make([]int32, n)
	var state uint32 = 0x12345678
	for i := range out {
		state = state*1664525 + 1013904223
		// map to [-bound, bound]
		v := int32(state>>8) % (2*bound + 1)
		out[i] = v - bound
	}
	return out
}

func TestComputeLPCResidualsInvertsRestoreLPC(t *testing.T) {
	src := genSamples(256, 30000) // int16-ish range; residuals stay in int32

	cases := []struct {
		name   string
		coeffs []int32
		shift  int
	}{
		{"order1_shift3", []int32{7}, 3},
		{"order2_shift4", []int32{-5, 2}, 4},
		{"order4_shift12", []int32{4096, -2048, 1024, -512}, 12},
		{"order2_shift0", []int32{1, 1}, 0},
		{"order8_shift10", []int32{900, -800, 700, -600, 500, -400, 300, -200}, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := len(tc.coeffs)
			res := make([]int32, len(src)-order)
			ComputeLPCResiduals(res, src, tc.coeffs, tc.shift, order)

			// Reconstruct: warmup verbatim, residuals in place, then RestoreLPC.
			dst := make([]int32, len(src))
			copy(dst[:order], src[:order])
			copy(dst[order:], res)
			RestoreLPC(dst, tc.coeffs, tc.shift, order)

			for i := range src {
				if dst[i] != src[i] {
					t.Fatalf("sample %d: got %d want %d", i, dst[i], src[i])
				}
			}
		})
	}
}

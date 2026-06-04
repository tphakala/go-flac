package lpc

import (
	"math/rand"
	"testing"
)

// TestFixedResidualsDiffParity proves the SIMD i32.Diff path is bit-identical to
// the scalar ComputeFixedResiduals (int64-accumulate then int32-truncate) for the
// residual region, and writes the warmup verbatim, across orders 1..4, several
// lengths, and magnitudes spanning the full int32 range including the extremes
// that exercise wraparound.
func TestFixedResidualsDiffParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5132))
	lengths := []int{5, 8, 9, 16, 33, 256, 4096}
	kinds := []struct {
		name string
		gen  func() int32
	}{
		{"quiet", func() int32 { return int32(rng.Intn(7) - 3) }},
		{"mid", func() int32 { return int32(rng.Intn(1<<17)) - (1 << 16) }},
		{"full", func() int32 { return int32(rng.Uint32()) }},
	}
	for _, n := range lengths {
		for _, kind := range kinds {
			src := make([]int32, n)
			for i := range src {
				src[i] = kind.gen()
			}
			// Seed extremes to force int32 wraparound through the predictor.
			src[0] = -2147483648
			if n > 1 {
				src[1] = 2147483647
			}
			if n > 2 {
				src[2] = -1
			}
			for order := 1; order <= 4 && order < n; order++ {
				dst := make([]int32, n)
				FixedResidualsDiff(dst, src, order)

				want := make([]int32, n-order)
				ComputeFixedResiduals(want, src, order)

				// Warmup region: verbatim copy of src.
				for i := range order {
					if dst[i] != src[i] {
						t.Fatalf("n=%d %s order=%d: warmup dst[%d]=%d, want src=%d",
							n, kind.name, order, i, dst[i], src[i])
					}
				}
				// Residual region: dst[order:] must equal the scalar residuals.
				for i := range want {
					if dst[order+i] != want[i] {
						t.Fatalf("n=%d %s order=%d: residual dst[%d]=%d, want %d",
							n, kind.name, order, order+i, dst[order+i], want[i])
					}
				}
			}
		}
	}
}

// TestLPCResidualsEncodeParity proves the SIMD i32.LPCResidualEncode path is
// bit-identical to the scalar ComputeLPCResiduals (the exact inverse of the
// decoder's RestoreLPC, so this is the round-trip correctness anchor) for the
// residual region, across orders up to FLAC's max, several shifts, and full-range
// int32 inputs including wraparound extremes.
func TestLPCResidualsEncodeParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x10C))
	lengths := []int{8, 9, 16, 33, 256, 4096}
	orders := []int{1, 2, 4, 8, 12, 32}
	shifts := []int{0, 5, 14, 15, 31}
	for _, n := range lengths {
		src := make([]int32, n)
		for i := range src {
			src[i] = int32(rng.Uint32())
		}
		src[0] = -2147483648
		if n > 1 {
			src[1] = 2147483647
		}
		for _, order := range orders {
			if order >= n {
				continue
			}
			qcoeff := make([]int32, order)
			for j := range qcoeff {
				qcoeff[j] = int32(rng.Intn(1<<15) - (1 << 14)) // 15-bit signed coeffs
			}
			for _, shift := range shifts {
				dst := make([]int32, n)
				LPCResidualsEncode(dst, src, qcoeff, shift)

				want := make([]int32, n-order)
				ComputeLPCResiduals(want, src, qcoeff, shift, order)

				for i := range order {
					if dst[i] != src[i] {
						t.Fatalf("n=%d order=%d shift=%d: warmup dst[%d]=%d, want src=%d",
							n, order, shift, i, dst[i], src[i])
					}
				}
				for i := range want {
					if dst[order+i] != want[i] {
						t.Fatalf("n=%d order=%d shift=%d: residual dst[%d]=%d, want %d",
							n, order, shift, order+i, dst[order+i], want[i])
					}
				}
			}
		}
	}
}

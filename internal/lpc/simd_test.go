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

// TestRestoreFixed32Parity proves the in-place SIMD RestoreFixed32 reconstructs a
// subframe bit-identically to the scalar RestoreFixed (the int64-accumulate loop)
// for every input, across orders 1..4, lengths straddling the SIMD block sizes
// including all-warmup short inputs, and magnitudes spanning the full int32 range.
func TestRestoreFixed32Parity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7ac1))
	lengths := []int{1, 2, 3, 4, 5, 8, 9, 16, 33, 256, 4096}
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
			// buf represents a decoded subframe: [warmup | residual], arbitrary.
			buf := make([]int32, n)
			for i := range buf {
				buf[i] = kind.gen()
			}
			buf[0] = -2147483648
			if n > 1 {
				buf[1] = 2147483647
			}
			if n > 2 {
				buf[2] = -1
			}
			for order := 1; order <= 4; order++ {
				want := make([]int32, n)
				copy(want, buf)
				RestoreFixed(want, order) // scalar, in place

				got := make([]int32, n)
				copy(got, buf)
				RestoreFixed32(got, order) // SIMD, in place

				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("n=%d %s order=%d: RestoreFixed32[%d]=%d, want %d",
							n, kind.name, order, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestRestoreLPC32Parity proves the in-place SIMD RestoreLPC32 reconstructs a
// subframe bit-identically to the scalar RestoreLPC (int64 accumulate, arithmetic
// >>shift of the full sum, int32-truncated wraparound add) for every input,
// across orders 1..32 (straddling the SIMD gate at 8), the shift range FLAC
// emits, lengths at and beyond the order, and full-range magnitudes.
func TestRestoreLPC32Parity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2f5e))
	orders := []int{1, 2, 4, 7, 8, 9, 12, 16, 31, 32}
	lengths := []int{1, 2, 5, 8, 9, 16, 33, 64, 256, 4096}
	shifts := []int{0, 1, 9, 14, 15}
	for _, order := range orders {
		coeffs := make([]int32, order)
		for j := range coeffs {
			coeffs[j] = int32(rng.Intn(1<<15) - (1 << 14)) // 15-bit signed qlp coeffs
		}
		for _, n := range lengths {
			buf := make([]int32, n)
			for i := range buf {
				buf[i] = int32(rng.Uint32())
			}
			if n > 2 {
				buf[0], buf[1], buf[2] = -2147483648, 2147483647, -1
			}
			for _, shift := range shifts {
				want := make([]int32, n)
				copy(want, buf)
				RestoreLPC(want, coeffs, shift, order) // scalar, in place

				got := make([]int32, n)
				copy(got, buf)
				RestoreLPC32(got, coeffs, shift) // SIMD, in place

				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("order=%d n=%d shift=%d: RestoreLPC32[%d]=%d, want %d",
							order, n, shift, i, got[i], want[i])
					}
				}
			}
		}
	}
}

// TestRestoreFixed32PanicsOutOfRange documents that RestoreFixed32 rejects orders
// outside [1,4]; order 0 is a no-op handled by the caller, not this function.
func TestRestoreFixed32PanicsOutOfRange(t *testing.T) {
	for _, order := range []int{0, 5} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("order=%d: expected panic, got none", order)
				}
			}()
			RestoreFixed32(make([]int32, 8), order)
		}()
	}
}

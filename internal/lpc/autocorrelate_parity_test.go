package lpc

import (
	"math"
	"math/rand"
	"testing"

	"github.com/tphakala/simd/f64"
)

// TestAutocorrelateSIMDParity asserts the SIMD-accelerated f64.Autocorrelate
// used by AnalyzeLPC is bit-for-bit identical to the scalar autocorrelateInto
// reference across realistic windowed blocks and the full LPC order range. This
// is the contract that keeps the quantized LPC coefficients, and therefore the
// encoded stream, byte-identical whether or not SIMD is active.
//
// Run it both ways to cover the kernel and the pure-Go fallback:
//
//	go test ./internal/lpc/
//	GODEBUG=cpu.avx2=off go test ./internal/lpc/   (amd64: forces the fallback)
func TestAutocorrelateSIMDParity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x10C))
	// Block sizes span the FLAC subset (and the short-block fallback); maxLag
	// spans every supported LPC order plus the multiples-of-4/2 lane boundaries.
	blockSizes := []int{1, 2, 3, 4, 16, 31, 32, 33, 192, 576, 1152, 4096, 4608}
	maxLags := []int{0, 1, 2, 3, 4, 8, 11, 12, 16, 31, 32}

	for _, n := range blockSizes {
		// Windowed PCM: integer samples shaped by a Tukey(0.5) window, exactly
		// what AnalyzeLPC feeds the autocorrelation.
		win := TukeyWindow(n, 0.5)
		for range 25 {
			x := make([]float64, n)
			for i := range x {
				s := float64(rng.Intn(1<<24) - (1 << 23)) // up to 24-bit PCM
				x[i] = s * win[i]
			}
			for _, maxLag := range maxLags {
				if maxLag >= n {
					continue
				}
				want := make([]float64, maxLag+1)
				autocorrelateInto(want, x, maxLag)

				got := make([]float64, maxLag+1)
				f64.Autocorrelate(got, x, maxLag)

				for lag := range want {
					if math.Float64bits(got[lag]) != math.Float64bits(want[lag]) {
						t.Fatalf("n=%d maxLag=%d lag=%d: SIMD=%v (%#x) scalar=%v (%#x)",
							n, maxLag, lag, got[lag], math.Float64bits(got[lag]),
							want[lag], math.Float64bits(want[lag]))
					}
				}
			}
		}
	}
}

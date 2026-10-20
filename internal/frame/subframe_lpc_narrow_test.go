package frame

import (
	"slices"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// TestDecodeLPCSubframe_WideRouteAtPredicateEdge decodes a hand-built 16-bit LPC
// subframe whose coefficients sum to |A| = 2^16 with every warm-up sample at the
// minimum. The unshifted prediction of the first predicted sample is exactly
// 2^31, which an int32 accumulator would wrap, so decodeLPC must hand the
// subframe's own bit width to the narrow predicate and take the int64 route. The
// residuals are computed with a plain int64 recurrence so that every restored
// sample is a known in-range value (zero). A wrong route shows up as a wrong
// sample, and the int64 decoder is the reference.
func TestDecodeLPCSubframe_WideRouteAtPredicateEdge(t *testing.T) {
	const (
		bps   = 16
		order = 4
		n     = 12
		shift = 1
	)
	warm := slices.Repeat([]int64{-(1 << (bps - 1))}, order)
	want := append(slices.Clone(warm), make([]int64, n-order)...) // predicted samples are all 0
	res := make([]int64, n-order)
	for i := order; i < n; i++ {
		var acc int64
		for j, c := range lpcWideRowCoeffs {
			acc += int64(c) * want[i-1-j]
		}
		res[i-order] = want[i] - acc>>shift
	}
	const edge = int64(1) << 30
	if res[0] != -edge {
		t.Fatalf("first residual = %d, want %d: the fixture no longer sits at the predicate edge", res[0], -edge)
	}

	bw := bitio.NewWriter()
	writeRawLPC(bw, bps, warm, shift, lpcWideRowCoeffs, res)
	bw.AlignByte()
	raw := bw.Bytes()

	got32 := decodeOneSubframe(t, raw, n, bps)
	got64 := decodeOneSubframe64(t, raw, n, bps)
	for i := range n {
		if int64(got32[i]) != want[i] || got64[i] != want[i] {
			t.Fatalf("sample %d: int32 path %d, int64 path %d, want %d", i, got32[i], got64[i], want[i])
		}
	}
}

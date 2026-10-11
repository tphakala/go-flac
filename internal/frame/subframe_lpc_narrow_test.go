package frame

import (
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// TestDecodeLPCSubframe_WideRouteAtPredicateEdge decodes a hand-built 16-bit LPC
// subframe whose coefficients sum to |A| = 2^16 with every warm-up sample at the
// minimum. The unshifted prediction is exactly 2^31, which an int32 accumulator
// would wrap, so decodeLPC must hand the subframe's own bit width to the narrow
// predicate and take the int64 route. The int64 decoder is the reference for the
// first predicted sample.
func TestDecodeLPCSubframe_WideRouteAtPredicateEdge(t *testing.T) {
	const (
		bps   = 16
		order = 4
		n     = 12
	)
	bw := bitio.NewWriter()
	bw.WriteBits(0, 1)          // subframe padding bit
	bw.WriteBits(32+order-1, 6) // LPC, order 4
	bw.WriteBits(0, 1)          // no wasted bits
	for range order {           // warm-up at the minimum sample
		bw.WriteSignedBits(-(1 << (bps - 1)), bps)
	}
	bw.WriteBits(14, 4)      // qlp precision 15
	bw.WriteSignedBits(1, 5) // shift 1
	for range order {        // 4 * 16384 = 2^16
		bw.WriteSignedBits(-(1 << 14), 15)
	}
	bw.WriteBits(0, 2)    // Rice, 4-bit parameters
	bw.WriteBits(0, 4)    // one partition
	bw.WriteBits(0, 4)    // Rice parameter 0
	for range n - order { // residual 0: unary "1"
		bw.WriteBits(1, 1)
	}
	bw.AlignByte()
	raw := bw.Bytes()

	got32 := decodeOneSubframe(t, raw, n, bps)
	got64 := decodeOneSubframe64(t, raw, n, bps)
	if want := int64(1) << 30; got64[order] != want {
		t.Fatalf("int64 reference sample %d = %d, want %d", order, got64[order], want)
	}
	// Later samples leave the 16-bit range, which the 64-bit decoder keeps
	// without wrapping, so only the warm-up and the first predicted sample are
	// comparable.
	for i := 0; i <= order; i++ {
		if int64(got32[i]) != got64[i] {
			t.Fatalf("sample %d: int32 path %d, int64 path %d", i, got32[i], got64[i])
		}
	}
}

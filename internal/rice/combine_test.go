package rice

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// TestWritePlannedCombinePathsRoundTrip drives WritePlanned with hand-built
// single-partition plans so both the combined small-quotient path (one WriteBits
// per residual) and the large-quotient fallback (WriteUnary + WriteBits) are hit
// deterministically, regardless of how the planner would have chosen the
// parameter. Each case is verified by decoding the bytes back.
func TestWritePlannedCombinePathsRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		res   []int32
		param int
	}{
		// k>0, quotients tiny: every residual takes the combined single-call path.
		{"combineSmallQuotient", []int32{0, 1, -1, 2, -2, 3, -3, 4}, 3},
		// k=0 with a value whose zigzag quotient (200) exceeds the combine width
		// ceiling, forcing the unary+remainder fallback for that residual.
		{"fallbackLargeQuotient", []int32{0, 1, -1, 100, -100, 0, 2, -2}, 0},
		// Mixed: small entries combine, the 100 entry falls back, in one partition.
		{"mixed", []int32{0, 0, 100, 1, -1, 2, -2, 0}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bw := bitio.NewWriter()
			plans := []PartPlan{{escape: false, param: c.param}}
			WritePlanned(bw, c.res, 0, len(c.res), plans, 4)
			bw.AlignByte()

			got := make([]int32, len(c.res))
			br := bitio.NewReader(bytes.NewReader(bw.Bytes()))
			if err := DecodeResidual(br, got, len(c.res), 0); err != nil {
				t.Fatalf("DecodeResidual: %v", err)
			}
			for i := range c.res {
				if got[i] != c.res[i] {
					t.Fatalf("residual[%d]=%d, want %d", i, got[i], c.res[i])
				}
			}
		})
	}
}

// TestWritePlanned64CombinePathsRoundTrip is the int64 analogue, also exercising a
// residual wider than 32 bits at a small parameter (large quotient fallback).
func TestWritePlanned64CombinePathsRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		res   []int64
		param int
	}{
		{"combineSmallQuotient", []int64{0, 1, -1, 2, -2, 3, -3, 4}, 3},
		{"fallbackLargeQuotient", []int64{0, 1, -1, 100, -100, 0, 2, -2}, 0},
		// A >32-bit value at a large parameter stays in the combined path (small
		// quotient), exercising a wide combined WriteBits width (~47 bits here).
		{"wideValueCombined", []int64{0, 1, -1, (1 << 33) - 1, -2, 0, 2, -2}, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bw := bitio.NewWriter()
			plans := []PartPlan{{escape: false, param: c.param}}
			WritePlanned64(bw, c.res, 0, len(c.res), plans, 5)
			bw.AlignByte()

			got := make([]int64, len(c.res))
			br := bitio.NewReader(bytes.NewReader(bw.Bytes()))
			if err := DecodeResidual(br, got, len(c.res), 0); err != nil {
				t.Fatalf("DecodeResidual: %v", err)
			}
			for i := range c.res {
				if got[i] != c.res[i] {
					t.Fatalf("residual[%d]=%d, want %d", i, got[i], c.res[i])
				}
			}
		})
	}
}

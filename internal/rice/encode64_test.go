package rice

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func TestZigzag64RoundTrip(t *testing.T) {
	cases := []int64{0, 1, -1, 2, -2, 1 << 31, -(1 << 31), (1 << 33) - 1, -(1 << 33)}
	for _, r := range cases {
		u := zigzag64(r)
		got := int64(u>>1) ^ -int64(u&1)
		if got != r {
			t.Fatalf("zigzag64(%d): u=%d back=%d", r, u, got)
		}
	}
}

// A partition holding a residual wider than 31 bits must NOT pick escape (the 5-bit
// width field cannot represent it); it must still encode and round-trip.
func TestEncodeResidual64WideRoundTrip(t *testing.T) {
	res := make([]int64, 16)
	for i := range res {
		res[i] = int64(i) - 8
	}
	res[7] = (1 << 33) - 1 // 34-bit zigzag, un-escape-able
	bw := bitio.NewWriter()
	bw.Reset()
	var sc Scratch
	wrote := EncodeResidual64(bw, res, len(res), 0, 0, &sc)
	if wrote <= 0 {
		t.Fatalf("EncodeResidual64 wrote %d bits", wrote)
	}
	cost := CostResidual64(res, len(res), 0, 0, &sc)
	if cost != wrote {
		t.Fatalf("CostResidual64 %d != EncodeResidual64 %d", cost, wrote)
	}
	bw.AlignByte()
	br := bitio.NewReader(bytesReader(bw.Bytes()))
	dst := make([]int64, len(res))
	if err := DecodeResidual(br, dst, len(res), 0); err != nil {
		t.Fatalf("DecodeResidual: %v", err)
	}
	for i := range res {
		if dst[i] != res[i] {
			t.Fatalf("residual[%d] = %d, want %d", i, dst[i], res[i])
		}
	}
}

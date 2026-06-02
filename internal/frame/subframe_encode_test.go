package frame

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

func decodeOneSubframe(t *testing.T, raw []byte, n, bps int) []int32 {
	t.Helper()
	got := make([]int32, n)
	br := bitio.NewReader(bytes.NewReader(raw))
	if err := decodeSubframe(br, got, bps); err != nil {
		t.Fatalf("decodeSubframe: %v", err)
	}
	return got
}

func TestWriteConstantRoundTrip(t *testing.T) {
	const bps = 16
	want := make([]int32, 100)
	for i := range want {
		want[i] = -1234
	}
	bw := bitio.NewWriter()
	writeConstant(bw, want[0], 0, bps)
	bw.AlignByte()
	got := decodeOneSubframe(t, bw.Bytes(), len(want), bps)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%d, want %d", i, got[i], want[i])
		}
	}
}

func TestWriteVerbatimRoundTripWithWasted(t *testing.T) {
	const bps = 16
	want := []int32{4, -8, 12, 0, 16, -4, 20, 8} // common factor 4 -> wasted 2
	w := wastedBits(want)
	if w != 2 {
		t.Fatalf("wastedBits=%d, want 2", w)
	}
	bw := bitio.NewWriter()
	writeVerbatim(bw, want, w, bps)
	bw.AlignByte()
	got := decodeOneSubframe(t, bw.Bytes(), len(want), bps)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%d, want %d", i, got[i], want[i])
		}
	}
}

func TestWastedBitsAllZeroIsZero(t *testing.T) {
	if w := wastedBits([]int32{0, 0, 0}); w != 0 {
		t.Fatalf("wastedBits(all-zero)=%d, want 0", w)
	}
}

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

func roundTripSubframe(t *testing.T, s []int32, bps int, p Params) {
	t.Helper()
	bw := bitio.NewWriter()
	var ws Workspace
	plan := planSubframe(&ws, s, bps, p, nil)
	writeSubframe(bw, &ws, s, bps, plan, p)
	bw.AlignByte()
	got := decodeOneSubframe(t, bw.Bytes(), len(s), bps)
	for i := range s {
		if got[i] != s[i] {
			t.Fatalf("kind=%d order=%d: got[%d]=%d, want %d", plan.kind, plan.order, i, got[i], s[i])
		}
	}
}

func TestPlanSubframeRoundTrip(t *testing.T) {
	p := Params{MaxPartitionOrder: 4}
	const bps = 16
	// linear ramp -> fixed predictor
	ramp := make([]int32, 1024)
	for i := range ramp {
		ramp[i] = int32(i*5 - 2000)
	}
	roundTripSubframe(t, ramp, bps, p)

	// constant -> constant subframe
	constSig := make([]int32, 512)
	for i := range constSig {
		constSig[i] = 777
	}
	roundTripSubframe(t, constSig, bps, p)

	// pseudo-random -> likely verbatim/high-order; must still round-trip
	noise := make([]int32, 777)
	x := int32(12345)
	for i := range noise {
		x = x*1103515245 + 12345
		noise[i] = (x >> 8) % 30000
	}
	roundTripSubframe(t, noise, bps, p)

	// exhaustive-fixed path
	roundTripSubframe(t, ramp, bps, Params{MaxPartitionOrder: 6, ExhaustiveFixed: true})

	// 24-bit and 8-bit depths
	roundTripSubframe(t, ramp, 24, p)
	small := make([]int32, 300)
	for i := range small {
		small[i] = int32(i%200 - 100)
	}
	roundTripSubframe(t, small, 8, p)
}

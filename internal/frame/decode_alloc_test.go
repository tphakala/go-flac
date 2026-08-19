package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// TestDecodeReusedFrameZeroTapAllocation proves the frame CRC tap allocates
// nothing per frame. It encodes one Rice-coded stereo frame (non-constant PCM, so
// the decode path runs real ReadUnary), then decodes it repeatedly by rebinding a
// single reused Reader and Frame. After the warm-up decode sizes the Frame's reuse
// buffers, a closure-free tap leaves steady-state decode at zero allocations; the
// old per-frame CRC-tap closures showed up here as a non-zero allocs/op.
func TestDecodeReusedFrameZeroTapAllocation(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	const bs = 512
	p := Params{Stereo: StereoFull, MaxPartitionOrder: 4, MaxLPCOrder: 8, LPCPrecision: 14, ExhaustiveFixed: true}

	// Deterministic wandering-plus-noise PCM, bounded well within 16-bit range, so
	// the encoder emits Rice-coded residuals rather than a CONSTANT subframe.
	l := make([]int32, bs)
	r := make([]int32, bs)
	s := uint32(1)
	nxt := func() int32 { s = s*1664525 + 1013904223; return int32(s>>15) % 200 }
	for i := range l {
		ramp := int32(i * 6)
		l[i] = ramp + nxt() - 100
		r[i] = -ramp + nxt() - 100
	}
	bw := bitio.NewWriter()
	enc := EncodeFrame(bw, NewWorkspace(bs, 2, 8), p, si, [][]int32{l, r}, 0)
	frameBytes := bytes.Clone(enc) // copy: enc aliases bw's buffer

	rd := bytes.NewReader(frameBytes)
	br := bitio.NewReader(rd)
	var fr Frame
	if err := Decode(br, si, &fr); err != nil { // warm-up: size fr's reuse buffers
		t.Fatalf("warm-up decode: %v", err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		rd.Reset(frameBytes)
		br.Reset(rd)
		if err := Decode(br, si, &fr); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("reused frame decode allocated %.0f objects/op; the CRC tap must be closure-free (want 0)", allocs)
	}
}

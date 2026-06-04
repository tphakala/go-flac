package frame

import (
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// TestWorkspaceLPCNoAlloc verifies that the int32 LPC encode path (compression
// levels 3-8) allocates nothing per frame once the Workspace and bit writer are
// warm. EncodeFrame resets bw and returns a slice aliasing bw's buffer, so a
// warmed bw plus the workspace-owned LPC scratch, apodization-window cache, and
// cost-eval residual buffer should drive steady-state allocations to zero.
func TestWorkspaceLPCNoAlloc(t *testing.T) {
	const bs = 4096
	ws := NewWorkspace(bs, 2, 12)
	bw := bitio.NewWriter()
	p := Params{
		Stereo:            StereoFull,
		MaxPartitionOrder: 6,
		ExhaustiveFixed:   true,
		MaxLPCOrder:       12,
		LPCPrecision:      15,
	}
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}

	// Two correlated channels: a slowly varying waveform plus a small per-channel
	// offset, so LPC finds a real predictor (constant/verbatim would not exercise
	// the analysis path).
	l := make([]int32, bs)
	r := make([]int32, bs)
	var acc int32
	for i := range bs {
		acc += int32(i%7) - 3
		l[i] = acc
		r[i] = acc - int32(i%5)
	}
	ch := [][]int32{l, r}

	// Warm bw (grows its buffer once) and ws (caches the window for this block).
	_ = EncodeFrame(bw, ws, p, si, ch, 0)

	got := testing.AllocsPerRun(20, func() {
		_ = EncodeFrame(bw, ws, p, si, ch, 1)
	})
	if got != 0 {
		t.Fatalf("EncodeFrame allocated %.0f times/op warm, want 0", got)
	}
}

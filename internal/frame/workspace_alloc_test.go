package frame

import (
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// TestWorkspaceWideNoAlloc verifies that the int64 wide (25-32 bps) encode path
// allocates nothing per frame once the Workspace and bit writer are warm.
// The channel data includes a low-bit noise term so wasted-bits is always 0,
// which means shifted64 is never needed here; the test instead exercises the
// costRes64 and l64/r64 workspace buffers introduced in Task 6. A separate path
// through wasted>0 is covered by TestEncoderByteIdenticalGolden's wide cases.
func TestWorkspaceWideNoAlloc(t *testing.T) {
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
	si := flac.StreamInfo{SampleRate: 192000, Channels: 2, BitDepth: 32}

	// Two correlated channels with a slowly varying waveform. Bit-1 noise ensures
	// wastedBits64 returns 0, so the test exercises costRes64 and l64/r64 rather
	// than only the wasted==0 fast path that returns s directly for shifted.
	l := make([]int32, bs)
	r := make([]int32, bs)
	var acc int32
	for i := range bs {
		acc += int32(i%7) - 3
		l[i] = (acc << 8) | 1 // low-bit noise: wasted bits = 0
		r[i] = ((acc - int32(i%5)) << 8) | 1
	}
	ch := [][]int32{l, r}

	// Warm bw (grows its buffer once) and ws (caches the window for this block).
	_ = EncodeFrame(bw, ws, p, si, ch, 0)

	got := testing.AllocsPerRun(20, func() {
		_ = EncodeFrame(bw, ws, p, si, ch, 1)
	})
	if got != 0 {
		t.Fatalf("EncodeFrame allocated %.0f times/op warm (wide 32 bps), want 0", got)
	}
}

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

package frame

import (
	"bytes"
	"math"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// decodeOneSubframe64 decodes a subframe through the int64 wide path. That path
// keeps the scalar restore (the shims' type assertion routes []int64 to
// lpc.RestoreFixed/RestoreLPC), so it is the oracle for the int32 SIMD path.
func decodeOneSubframe64(t *testing.T, raw []byte, n, bps int) []int64 {
	t.Helper()
	got := make([]int64, n)
	br := bitio.NewReader(bytes.NewReader(raw))
	if err := decodeSubframe64(br, got, bps); err != nil {
		t.Fatalf("decodeSubframe64: %v", err)
	}
	return got
}

// TestDecodeSubframeSIMDMatchesScalar encodes representative subframes with the
// int32 encoder, then decodes the identical bytes through decodeSubframe (int32,
// wired to the SIMD restore kernels) and decodeSubframe64 (int64, scalar restore).
// The two must agree sample-for-sample and both must reconstruct the original
// samples. This pins the wired int32 restore path bit-exact to the scalar
// reference across FIXED orders 0..4 and LPC orders that straddle the SIMD gate
// at 8 (tonal signals select high-order LPC).
func TestDecodeSubframeSIMDMatchesScalar(t *testing.T) {
	const n = 4096
	// Signals are amplitude-normalized to func(i, amp); the caller scales amp to
	// the sample range so every generated value stays in [-amp, amp] and round
	// trips losslessly at the given bps.
	signals := []struct {
		name string
		gen  func(i int, amp float64) int32
	}{
		{"constant", func(i int, amp float64) int32 { return int32(amp / 8) }},
		{"ramp", func(i int, amp float64) int32 { return int32((float64(i)/n*2 - 1) * amp) }},
		{"parabola", func(i int, amp float64) int32 {
			x := (float64(i) - n/2) / (n / 2)
			return int32(x * x * amp)
		}},
		{"tone-low", func(i int, amp float64) int32 { return int32(amp * 0.9 * math.Sin(float64(i)*0.02)) }},
		{"tone-high", func(i int, amp float64) int32 { return int32(amp * 0.9 * math.Sin(float64(i)*0.31)) }},
		{"two-tone", func(i int, amp float64) int32 {
			return int32(amp * (0.6*math.Sin(float64(i)*0.05) + 0.4*math.Sin(float64(i)*0.17)))
		}},
	}
	p := paramsLevel(t, 8) // LPC enabled, so tonal signals pick LPC
	var sawFixed bool      // kind 2, order 1..4
	var maxLPCOrder int    // kind 3
	for _, bps := range []int{16, 24} {
		amp := float64(int32(1)<<(bps-1)) - 1 // peak magnitude that fits the bps range
		for _, sig := range signals {
			s := make([]int32, n)
			for i := range s {
				s[i] = sig.gen(i, amp)
			}
			ws := NewWorkspace(n, 1, 32)
			window := ws.window(p, n)
			bw := bitio.NewWriter()
			plan := planSubframe(ws, 0, s, bps, p, window)
			writeSubframe(bw, ws, s, bps, &plan, p)
			bw.AlignByte()
			raw := bw.Bytes()

			if plan.kind == 2 && plan.order >= 1 {
				sawFixed = true
			}
			if plan.kind == 3 && plan.order > maxLPCOrder {
				maxLPCOrder = plan.order
			}

			got32 := decodeOneSubframe(t, raw, n, bps)
			got64 := decodeOneSubframe64(t, raw, n, bps)
			for i := range s {
				if int64(got32[i]) != got64[i] {
					t.Fatalf("bps=%d %s (kind=%d order=%d)[%d]: int32=%d, int64=%d",
						bps, sig.name, plan.kind, plan.order, i, got32[i], got64[i])
				}
				if int64(got32[i]) != int64(s[i]) {
					t.Fatalf("bps=%d %s (kind=%d order=%d)[%d]: decoded=%d, want original %d",
						bps, sig.name, plan.kind, plan.order, i, got32[i], s[i])
				}
			}
		}
	}
	// Guard the test's own reach: if signal selection drifts so the SIMD LPC
	// kernel (order >= 8) or the fixed path is no longer exercised, this parity
	// test would silently stop covering the wired code.
	if !sawFixed {
		t.Fatal("no FIXED subframe exercised; parity test lost fixed-restore coverage")
	}
	if maxLPCOrder < 8 {
		t.Fatalf("max LPC order %d < 8; parity test never hit the SIMD LPC kernel gate", maxLPCOrder)
	}
}

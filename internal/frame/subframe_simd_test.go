package frame

import (
	"bytes"
	"math"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/i32"
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
// reference across FIXED orders 0..4 and LPC orders on both sides of the
// scalar/SIMD cutover where the build and CPU have one (tonal signals select high-order LPC),
// on both the int64 and the int32-accumulating restore routes.
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
		// Several tones plus a little noise: at 24 bps this selects a high LPC
		// order whose coefficients fail the narrow predicate, so it reaches the
		// int64 SIMD restore kernel that the 16-bit signals (which pass it) no
		// longer do.
		{"multi-tone-noise", func(i int, amp float64) int32 {
			x := uint32(i)*2654435761 ^ 0x9e3779b9
			x ^= x >> 15
			nz := float64(int32(x)) / float64(1<<31)
			v := 0.0
			for k := 1; k <= 7; k++ {
				v += math.Sin(float64(i)*0.037*float64(k)+float64(k)) / float64(k+1)
			}
			return int32(amp * (0.3*v + 0.0005*nz))
		}},
	}
	p := paramsLevel(t, 8) // LPC enabled, so tonal signals pick LPC
	var sawFixed bool      // kind 2, order 1..4
	// Highest LPC order reached by subframes the decoder restores with int32
	// accumulation (narrow) and with int64 accumulation (wide), classified by the
	// predicate the decoder uses; -1 until one of that kind is seen.
	maxNarrowOrder, maxWideOrder := -1, -1
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
			if plan.kind == 3 {
				coeffs := plan.qcoeff[:plan.order]
				if i32.LPCRestoreNarrowSafe(coeffs, uint(bps-plan.wasted), uint(plan.shift)) {
					maxNarrowOrder = max(maxNarrowOrder, plan.order)
				} else {
					maxWideOrder = max(maxWideOrder, plan.order)
				}
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
	// Guard the test's own reach: if signal selection drifts so the fixed path or
	// the SIMD LPC restore kernel (orders from i32.FirstSIMDRestoreOrder, which
	// tracks the per-architecture scalar ceiling and CPU features) is no longer
	// exercised, this parity test would silently stop covering the wired code. The
	// SIMD kernel is the int64 one, reached only by subframes that fail the narrow
	// predicate, and the narrow route needs subframes that pass it.
	if !sawFixed {
		t.Fatal("no FIXED subframe exercised; parity test lost fixed-restore coverage")
	}
	if maxNarrowOrder < 0 || maxWideOrder < 0 {
		t.Fatalf("LPC subframes: max narrow order %d, max wide order %d; the test must reach both restore routes", maxNarrowOrder, maxWideOrder)
	}
	if maxNarrowOrder < 1 {
		t.Fatal("no narrow-eligible LPC subframe; parity test lost int32-accumulation coverage")
	}
	first := i32.FirstSIMDRestoreOrder()
	if first == 0 {
		t.Skip("no SIMD LPC restore kernel on this build or CPU; parity checked, SIMD reach guard skipped")
	}
	if maxWideOrder < first {
		t.Fatalf("max wide LPC order %d < %d; parity test never reached the SIMD LPC restore kernel", maxWideOrder, first)
	}
}

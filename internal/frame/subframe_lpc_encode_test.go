package frame

import (
	"bytes"
	"math"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/lpc"
)

func arSignal16(n int) []int32 {
	out := make([]int32, n)
	var p1, p2 float64
	var state uint32 = 0x13572468
	for i := range out {
		state = state*1664525 + 1013904223
		noise := (float64(int32(state>>9)%401) - 200)
		v := 1.6*p1 - 0.7*p2 + noise
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int32(math.Round(v))
		p2 = p1
		p1 = v
	}
	return out
}

func TestPlanSubframeSelectsLPC(t *testing.T) {
	s := arSignal16(4096)
	p := Params{MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 12, LPCPrecision: 15}
	window := lpc.TukeyWindow(len(s), 0.5)

	plan := planSubframe(s, 16, p, window)
	if plan.kind != 3 {
		t.Fatalf("plan.kind = %d, want 3 (LPC) for a strongly correlated signal", plan.kind)
	}
	if plan.order < 1 || plan.order > 12 {
		t.Fatalf("plan.order = %d out of [1,12]", plan.order)
	}
	if len(plan.qcoeff) != plan.order {
		t.Fatalf("len(qcoeff) = %d, want %d", len(plan.qcoeff), plan.order)
	}
	if plan.shift < 0 || plan.shift > 15 {
		t.Fatalf("plan.shift = %d out of [0,15]", plan.shift)
	}
}

func TestPlanSubframeNoLPCWhenDisabled(t *testing.T) {
	s := arSignal16(4096)
	p := Params{MaxPartitionOrder: 3} // MaxLPCOrder 0 -> fixed only
	plan := planSubframe(s, 16, p, nil)
	if plan.kind == 3 {
		t.Fatal("plan.kind = 3 (LPC) but MaxLPCOrder is 0")
	}
}

func TestWriteLPCRoundTrips(t *testing.T) {
	s := arSignal16(4096)
	bps := 16
	p := Params{MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 12, LPCPrecision: 15}
	window := lpc.TukeyWindow(len(s), 0.5)

	plan := planSubframe(s, bps, p, window)
	if plan.kind != 3 {
		t.Fatalf("precondition: expected LPC plan, got kind %d", plan.kind)
	}

	bw := bitio.NewWriter()
	writeSubframe(bw, s, bps, plan, p)
	bw.AlignByte()
	data := bw.Bytes()

	// The emitted subframe must genuinely be an LPC subframe (type code
	// 31+order), not a fixed subframe. Without writeLPC, kind 3 falls through to
	// writeFixed and emits type 8+order; this assertion fails for that wrong
	// path. The type code lives in bits 1..6 of the first byte (bit 0 is the
	// zero pad). This guards against an order that would otherwise round-trip
	// correctly through the fixed decoder.
	if got := int(data[0]>>1) & 0x3F; got != 31+plan.order {
		t.Fatalf("subframe type code = %d, want %d (LPC order %d)", got, 31+plan.order, plan.order)
	}

	// Decode the subframe back through the real M2 decoder entry and compare.
	br := bitio.NewReader(bytes.NewReader(data))
	got := make([]int32, len(s))
	if err := decodeSubframe(br, got, bps); err != nil {
		t.Fatalf("decodeSubframe: %v", err)
	}
	for i := range s {
		if got[i] != s[i] {
			t.Fatalf("round-trip mismatch at %d: got %d want %d", i, got[i], s[i])
		}
	}
}

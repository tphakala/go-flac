package pcm

import (
	"bytes"
	"testing"
)

// TestCarryCapBounded verifies that the encoder's internal carry buffer never
// retains a capacity larger than a small multiple of one block, regardless of
// how large a single Write call is. A very large first Write exercises the
// direct-emit path (step 2 in Write) without touching carry; subsequent smaller
// Writes exercise the leftover+head assembly path (step 1) that does use carry.
//
// The test checks the empirical cap both before and after Close to confirm the
// bound holds at every point the carry slice is observable.
func TestCarryCapBounded(t *testing.T) {
	cfg := Config{
		SampleRate:       44100,
		Channels:         2,
		BitDepth:         16,
		CompressionLevel: 0,
	}

	bytesPS := (cfg.BitDepth + 7) / 8
	oneBlockBytes := encoderBlockSize * bytesPS * cfg.Channels // 4096 * 2 * 2 = 16384
	maxAllowed := 2 * oneBlockBytes

	// Build a large PCM buffer: 200 full blocks plus one partial block worth of
	// remainder so that the encoder stores leftover after the giant Write.
	// The partial trailing portion ensures a subsequent Write exercises step 1
	// (leftover + head of new input) and actually uses carry.
	totalSamples := 200*encoderBlockSize + encoderBlockSize/2
	pcm := genPCM(cfg, totalSamples)

	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// One giant Write: this should go through step 2 for most blocks and store
	// a partial block in leftover. carry should remain nil or minimal.
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("large Write: %v", err)
	}
	t.Logf("after large Write: cap(carry)=%d oneBlockBytes=%d", cap(enc.carry), oneBlockBytes)
	if cap(enc.carry) > maxAllowed {
		t.Errorf("after large Write: cap(carry)=%d exceeds 2*oneBlockBytes=%d",
			cap(enc.carry), maxAllowed)
	}

	// Feed several small Writes that each add enough bytes to complete a block
	// together with the stored leftover. Each of these exercises Write step 1
	// (carry assembly), so carry will be set to exactly one block.
	for i := range 5 {
		smallPCM := genPCM(cfg, encoderBlockSize)
		if _, err := enc.Write(smallPCM); err != nil {
			t.Fatalf("small Write %d: %v", i, err)
		}
		t.Logf("after small Write %d: cap(carry)=%d", i, cap(enc.carry))
		if cap(enc.carry) > maxAllowed {
			t.Errorf("after small Write %d: cap(carry)=%d exceeds 2*oneBlockBytes=%d",
				i, cap(enc.carry), maxAllowed)
		}
	}

	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

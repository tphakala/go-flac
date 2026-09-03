package pcm

import (
	"bytes"
	"errors"
	"testing"
)

// encodeIncremental drives the incremental Write/Finalize API, feeding pcm in chunks
// of the given byte size, and returns the concatenated frame bytes, the total number
// of frames emitted (across Write and the Finalize flush), and the sum of the counts
// Write itself reported (which excludes the final short block that Finalize flushes).
func encodeIncremental(t *testing.T, cfg Config, pcm []byte, chunk int) (joined []byte, totalEmits, writeReported int) {
	t.Helper()
	e, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}
	emit := func(fr []byte, _ int) error {
		joined = append(joined, fr...)
		totalEmits++
		return nil
	}
	for off := 0; off < len(pcm); off += chunk {
		end := min(off+chunk, len(pcm))
		n, err := e.Write(pcm[off:end], emit)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		writeReported += n
	}
	if err := e.Finalize(emit); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return joined, totalEmits, writeReported
}

// TestFrameEncoderIncrementalMatchesOneShot pins the core guarantee: the incremental
// Write/Finalize path, fed in arbitrary chunk sizes (including ones that split
// mid-sample and mid-block), emits frames byte-identical to a single EncodeInterleaved
// of the same input. If the leftover/carry buffering were wrong, some chunking would
// diverge.
func TestFrameEncoderIncrementalMatchesOneShot(t *testing.T) {
	cfgs := []Config{
		{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5},
		{SampleRate: 48000, Channels: 1, BitDepth: 24, CompressionLevel: 8},
	}
	// A block is 4096*frameLen bytes; chunk sizes straddle sub-block, exact-block, and
	// multi-block, plus 1 and a prime to stress mid-sample splits.
	for _, cfg := range cfgs {
		pcm := genPCM(cfg, 4096*3+517)
		want, _, _ := collectFrames(t, cfg, pcm)
		var wantJoined []byte
		for _, fr := range want {
			wantJoined = append(wantJoined, fr...)
		}
		frameLen := (cfg.BitDepth / 8) * cfg.Channels
		blockBytes := 4096 * frameLen
		for _, chunk := range []int{1, 3, frameLen, frameLen + 1, 777, blockBytes - 1, blockBytes, blockBytes + 5, len(pcm)} {
			got, totalEmits, writeReported := encodeIncremental(t, cfg, pcm, chunk)
			if !bytes.Equal(got, wantJoined) {
				t.Fatalf("cfg %+v chunk %d: incremental frames differ from one-shot (%d vs %d bytes)", cfg, chunk, len(got), len(wantJoined))
			}
			// Every frame is emitted exactly once across Write and Finalize.
			if totalEmits != len(want) {
				t.Errorf("cfg %+v chunk %d: total frames emitted = %d, want %d", cfg, chunk, totalEmits, len(want))
			}
			// The input has a short final block (517 samples), so Write emits every frame
			// but that last one; Finalize flushes it.
			if writeReported != len(want)-1 {
				t.Errorf("cfg %+v chunk %d: Write reported %d frames, want %d (Finalize flushes the final short block)", cfg, chunk, writeReported, len(want)-1)
			}
		}
	}
}

// TestFrameEncoderIncrementalStreamInfoMatches checks that after Finalize the
// STREAMINFO (MD5, total samples, min/max frame sizes) equals what the one-shot
// encoder measures for the same input.
func TestFrameEncoderIncrementalStreamInfoMatches(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096*2+900)

	_, _, oneShot := collectFrames(t, cfg, pcm)

	e, _ := NewFrameEncoder(cfg)
	emit := func([]byte, int) error { return nil }
	if _, err := e.Write(pcm, emit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := e.Finalize(emit); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !bytes.Equal(e.StreamInfoBytes(), oneShot.StreamInfoBytes()) {
		t.Errorf("incremental STREAMINFO differs from one-shot")
	}
}

func TestFrameEncoderIncrementalGuards(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	noop := func([]byte, int) error { return nil }

	t.Run("finalize before write", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if err := e.Finalize(noop); err == nil {
			t.Error("expected error finalizing before any Write")
		}
	})

	t.Run("write after finalize", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if _, err := e.Write(genPCM(cfg, 4096), noop); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := e.Finalize(noop); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if _, err := e.Write(genPCM(cfg, 10), noop); err == nil {
			t.Error("expected error writing after Finalize")
		}
	})

	t.Run("double finalize", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if _, err := e.Write(genPCM(cfg, 100), noop); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := e.Finalize(noop); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if err := e.Finalize(noop); err == nil {
			t.Error("expected error on a second Finalize")
		}
	})

	t.Run("nil emit", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if _, err := e.Write(genPCM(cfg, 100), nil); err == nil {
			t.Error("expected error for a nil emit callback")
		}
	})

	t.Run("nil emit to finalize on a block-aligned stream", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		// A whole-block write leaves no trailing block, so Finalize would not otherwise
		// touch emit; the nil guard must still fire, or a nil callback hides here and
		// only surfaces on a stream that happens to end mid-block.
		if _, err := e.Write(genPCM(cfg, 4096), noop); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := e.Finalize(nil); err == nil {
			t.Error("expected error for a nil emit to Finalize even with no trailing block")
		}
	})

	t.Run("cannot mix one-shot after incremental", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if _, err := e.Write(genPCM(cfg, 100), noop); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := e.EncodeInterleaved(genPCM(cfg, 100), noop); err == nil {
			t.Error("expected error using EncodeInterleaved after Write")
		}
	})

	t.Run("cannot mix incremental after one-shot", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		if err := e.EncodeInterleaved(genPCM(cfg, 100), noop); err != nil {
			t.Fatalf("EncodeInterleaved: %v", err)
		}
		if _, err := e.Write(genPCM(cfg, 100), noop); err == nil {
			t.Error("expected error using Write after EncodeInterleaved")
		}
	})

	t.Run("partial trailing sample at finalize", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg) // frameLen = 4 bytes
		if _, err := e.Write([]byte{0, 0, 0, 0, 0, 0}, noop); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := e.Finalize(noop); err == nil {
			t.Error("expected error for a partial trailing interleaved sample")
		}
	})

	t.Run("total samples mismatch at finalize", func(t *testing.T) {
		c := cfg
		c.TotalSamples = 5000
		e, _ := NewFrameEncoder(c)
		if _, err := e.Write(genPCM(c, 4096), noop); err != nil { // 4096 != declared 5000
			t.Fatalf("Write: %v", err)
		}
		if err := e.Finalize(noop); err == nil {
			t.Error("expected error when encoded count disagrees with Config.TotalSamples")
		}
	})

	t.Run("emit error propagates from write", func(t *testing.T) {
		e, _ := NewFrameEncoder(cfg)
		sentinel := errTest
		_, err := e.Write(genPCM(cfg, 4096), func([]byte, int) error { return sentinel })
		if !errors.Is(err, sentinel) {
			t.Errorf("Write error = %v, want the emit sentinel", err)
		}
	})
}

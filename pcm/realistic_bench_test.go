package pcm

import (
	"math"
	"testing"
)

// genRealisticPCM mirrors scripts/bench-encoders.sh: a 440 Hz half-scale sine
// mixed with uniform noise (amplitude 0.25). Unlike genPCM's ramp (whose fixed
// residuals are ~0), this has real residual entropy, so the Rice search and LPC
// analysis carry their true cost and benchmark results are not inverted.
func genRealisticPCM(cfg Config, nSamples int) []byte {
	bytesPS := (cfg.BitDepth + 7) / 8
	full := int64(1) << (cfg.BitDepth - 1)
	out := make([]byte, 0, nSamples*cfg.Channels*bytesPS)
	x := uint64(88172645463325252)
	for i := range nSamples {
		tone := 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(cfg.SampleRate))
		for range cfg.Channels {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			noise := (float64(x>>11)/float64(1<<53))*0.5 - 0.25
			v := int64((tone + noise) * float64(full))
			if v >= full {
				v = full - 1
			}
			if v < -full {
				v = -full
			}
			u := uint64(v)
			for b := range bytesPS {
				out = append(out, byte(u>>(uint(b)*8)))
			}
		}
	}
	return out
}

func benchRealistic(b *testing.B, channels int) {
	b.Helper()
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: channels, CompressionLevel: 5}
	pcmBytes := genRealisticPCM(cfg, 48000*2)
	b.SetBytes(int64(len(pcmBytes)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var sink seekBuffer
		enc, err := NewEncoder(&sink, cfg)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := enc.Write(pcmBytes); err != nil {
			b.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeRealistic(b *testing.B)       { benchRealistic(b, 1) }
func BenchmarkEncodeRealisticStereo(b *testing.B) { benchRealistic(b, 2) }

// BenchmarkEncodeRealisticSteadyState measures steady-state per-block encode
// compute (stereo, level 5, realistic PCM), isolated from encoder construction
// cost. See issue #11: BenchmarkEncodeRealisticStereo above calls NewEncoder
// inside b.Loop(), so every iteration also pays for allocating the encoder's
// large scratch buffers (per-channel block buffers, the LPC workspace, the bit
// writer), which for this config runs to roughly 1-2 MB/op and lets the CPU
// profile be dominated by allocation and GC (madvise/kevent) rather than the
// encode compute the benchmark is meant to measure.
//
// The fix here is to build one Encoder before the timed loop and reuse it via
// Encoder.Reset for every iteration instead of calling NewEncoder inside the
// loop. Reset is documented as "essentially allocation-free" when the channel
// count and LPC order shape stays the same across calls, which holds here
// since every iteration reuses the exact same cfg; the timed region is
// therefore dominated by the real per-block work (block partitioning, fixed
// and LPC prediction, Rice parameter search, and bit packing) rather than by
// buffer construction. Close is called inside the loop too (it is what
// finalizes the block and flushes it through the bit writer), but since the
// encoder itself is never reallocated this stays cheap.
//
// The sink is a single seekBuffer, truncated back to zero length (not
// replaced) at the top of each iteration so its backing array is reused as
// well; a fresh seekBuffer per iteration would reintroduce exactly the kind
// of per-iteration allocation this benchmark exists to avoid.
func BenchmarkEncodeRealisticSteadyState(b *testing.B) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genRealisticPCM(cfg, 48000*2)

	var sink seekBuffer
	enc, err := NewEncoder(&sink, cfg)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(pcmBytes)))
	b.ReportAllocs()
	// b.Loop resets and pauses the timer around its own bookkeeping, so the setup
	// above (encoder construction, PCM generation) is already excluded; no explicit
	// b.ResetTimer is needed.
	for b.Loop() {
		sink.data = sink.data[:0]
		sink.pos = 0
		if err := enc.Reset(&sink, cfg); err != nil {
			b.Fatal(err)
		}
		if _, err := enc.Write(pcmBytes); err != nil {
			b.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

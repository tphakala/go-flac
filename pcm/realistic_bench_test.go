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

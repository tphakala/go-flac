package pcm

import "testing"

// BenchmarkEncode measures encode throughput on ~1s of 16-bit stereo at level 5.
func BenchmarkEncode(b *testing.B) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 44100)
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

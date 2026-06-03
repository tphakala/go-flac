package pcm

import (
	"bytes"
	"testing"
)

// encodeRampForBench is buildRamp specialized to the benchmark stream shape.
func encodeRampForBench(samples int) (data []byte, count int) {
	return buildRamp(2, 16, samples)
}

// encodeRampSeektableForBench encodes the benchmark ramp with a SEEKTABLE enabled. It
// panics on the encode errors that cannot occur for these fixed valid inputs.
func encodeRampSeektableForBench(samples, interval int) []byte {
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 2,
		SeekTableInterval: interval, SeekTableMaxPoints: 64,
	})
	if err != nil {
		panic(err)
	}
	if _, err := enc.Write(rawRamp(2, 16, samples)); err != nil {
		panic(err)
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	return sb.Bytes()
}

func benchSeek(b *testing.B, data []byte, target int64) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		dec, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := dec.SeekToSample(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSeekNoTable(b *testing.B) {
	data, total := encodeRampForBench(20 * 4096)
	benchSeek(b, data, int64(total)/2)
}

func BenchmarkSeekWithTable(b *testing.B) {
	data := encodeRampSeektableForBench(20*4096, 4096)
	benchSeek(b, data, 10*4096)
}

package pcm

import (
	"bytes"
	"testing"
)

// buildRamp is encodeRamp without a *testing.T, for use in fuzz seed construction
// where no test context exists. It panics on the encode errors that cannot occur for
// these fixed, valid inputs.
func buildRamp(channels, bps, samples int) (data []byte, count int) {
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{SampleRate: 44100, Channels: channels, BitDepth: bps, CompressionLevel: 2})
	if err != nil {
		panic(err)
	}
	bytesPS := (bps + 7) / 8
	pcm := make([]byte, samples*channels*bytesPS)
	idx := 0
	for s := range samples {
		for c := range channels {
			v := uint32(int32(s + c))
			for b := range bytesPS {
				pcm[idx] = byte(v >> (uint(b) * 8))
				idx++
			}
		}
	}
	if _, err := enc.Write(pcm); err != nil {
		panic(err)
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	return sb.Bytes(), samples
}

func FuzzSeek(f *testing.F) {
	base, total := buildRamp(2, 16, 4096*3+50)
	f.Add(base, int64(0))
	f.Add(base, int64(total)-1)
	f.Add(base, int64(total)+5)
	f.Fuzz(func(t *testing.T, data []byte, target int64) {
		dec, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			return // not a valid stream; nothing to seek
		}
		got, err := dec.SeekToSample(target)
		if err != nil {
			return
		}
		if got < 0 {
			t.Fatalf("seek returned negative sample %d", got)
		}
		if tot := int64(dec.Info().TotalSamples); tot > 0 && got > tot {
			t.Fatalf("seek returned %d > TotalSamples %d", got, tot)
		}
	})
}

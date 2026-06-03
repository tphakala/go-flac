package pcm

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// widePCMBytes builds interleaved 4-byte little-endian signed PCM whose per-sample
// values span the given bit depth. Uses int64 math so bitDepth 31 and 32 do not
// overflow (an int32-based 1<<(bd-1) would wrap negative and panic Int31n).
func widePCMBytes(frames, channels, bitDepth int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	half := int64(1) << (bitDepth - 1)
	out := make([]byte, frames*channels*4)
	idx := 0
	for range frames {
		for range channels {
			v := int32(r.Int63n(2*half) - half) // in [-2^(bd-1), 2^(bd-1)-1]
			u := uint32(v)
			out[idx] = byte(u)
			out[idx+1] = byte(u >> 8)
			out[idx+2] = byte(u >> 16)
			out[idx+3] = byte(u >> 24)
			idx += 4
		}
	}
	return out
}

func TestWideRoundTripAllLevels(t *testing.T) {
	const frames = 8192
	for _, bd := range []int{25, 28, 32} {
		for _, ch := range []int{1, 2} {
			for lvl := 0; lvl <= 8; lvl++ {
				pcmIn := widePCMBytes(frames, ch, bd, int64(bd*100+ch*10+lvl))

				var enc bytes.Buffer
				e, err := NewEncoder(&enc, Config{SampleRate: 96000, Channels: ch, BitDepth: bd, CompressionLevel: lvl})
				if err != nil {
					t.Fatalf("bd%d ch%d L%d NewEncoder: %v", bd, ch, lvl, err)
				}
				if _, err := e.Write(pcmIn); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if err := e.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}

				dec, err := NewDecoder(bytes.NewReader(enc.Bytes()))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcmOut, err := io.ReadAll(dec)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !bytes.Equal(pcmOut, pcmIn) {
					t.Fatalf("bd%d ch%d L%d: round-trip PCM mismatch (%d vs %d bytes)", bd, ch, lvl, len(pcmOut), len(pcmIn))
				}
			}
		}
	}
}

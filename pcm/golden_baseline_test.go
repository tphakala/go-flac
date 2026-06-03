package pcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// goldenCase returns the encoded FLAC bytes for one deterministic <= 24 bps config.
func goldenCase(t *testing.T, sampleRate, channels, bitDepth, level int) []byte {
	t.Helper()
	const frames = 4096
	bytesPS := (bitDepth + 7) / 8
	pcm := make([]byte, frames*channels*bytesPS)
	for i := range pcm {
		// uint32 arithmetic keeps the generator deterministic across word sizes, so
		// the golden hashes hold on 32-bit (GOARCH 386/arm) as well as 64-bit. The
		// extracted byte is unchanged on 64-bit (it reads bits below 2^32).
		pcm[i] = byte((uint32(i)*1103515245 + 12345) >> 7)
	}
	var out bytes.Buffer
	enc, err := NewEncoder(&out, Config{SampleRate: sampleRate, Channels: channels, BitDepth: bitDepth, CompressionLevel: level})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}

func TestInt32OutputUnchanged(t *testing.T) {
	want := map[string]string{
		"44100/1/16/5": "fac8ce89162247db6ff9ea876e79ff21615e0944dfadc1d195424071822d991d",
		"44100/2/16/5": "f895dc6923243681cc0a7773e11b9731117a316204f6646480fb4f1c3886a9dd",
		"48000/2/24/8": "497f00f0cf8f484008662efe8828a7e5d976a6998d4b99909aa6b7e141d24a1b",
		"44100/2/16/0": "653959c8a80348a20fd18dd2bde25f23ee02bccc039ce1fb3ac053a03bb77164",
		"96000/2/24/3": "ab69363491a90874336d46f0b6f378e206d8df4250636467a7e5775365cec98a",
	}
	cases := []struct{ sr, ch, bd, lvl int }{
		{44100, 1, 16, 5}, {44100, 2, 16, 5}, {48000, 2, 24, 8},
		{44100, 2, 16, 0}, {96000, 2, 24, 3},
	}
	for _, c := range cases {
		key := fmt.Sprintf("%d/%d/%d/%d", c.sr, c.ch, c.bd, c.lvl)
		got := sha256.Sum256(goldenCase(t, c.sr, c.ch, c.bd, c.lvl))
		if hex.EncodeToString(got[:]) != want[key] {
			t.Fatalf("int32 output for %s changed: got %s", key, hex.EncodeToString(got[:]))
		}
	}
}

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
		"44100/1/16/5": "2449f4597b5ce42c950f78ae85613ee9bd44b3a98222cef555b63780acb23b79",
		"44100/2/16/5": "8fe9c3a04de7da0885f4a664320e09c76038af96a494a1018be22256066bc61f",
		"48000/2/24/8": "35a07d967bc55f6c76fa879c28313a14e49ec60d3fd1c2f20908a80691c9b779",
		"44100/2/16/0": "8bab4d771c55eeb4a4b59fcfc72e4669bd3a9848cafd473f86492212755cce9e",
		"96000/2/24/3": "ee6ec91fe033a2cae8af887cce5e277301a3b8d792b308f346b78ad9b3ac2945",
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

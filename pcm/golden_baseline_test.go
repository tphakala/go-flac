package pcm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// goldenCase returns the encoded FLAC bytes for one deterministic <= 24 bps config.
func goldenCase(t *testing.T, sampleRate, channels, bitDepth, level int) []byte {
	t.Helper()
	const frames = 4096
	bytesPS := (bitDepth + 7) / 8
	pcm := make([]byte, frames*channels*bytesPS)
	for i := range pcm {
		pcm[i] = byte((i*1103515245 + 12345) >> 7)
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

// TestGoldenBaselineCapture records the current int32 output checksums. Task 12 turns
// these into assertions after the encoder refactor.
func TestGoldenBaselineCapture(t *testing.T) {
	cases := []struct{ sr, ch, bd, lvl int }{
		{44100, 1, 16, 5}, {44100, 2, 16, 5}, {48000, 2, 24, 8},
		{44100, 2, 16, 0}, {96000, 2, 24, 3},
	}
	for _, c := range cases {
		got := sha256.Sum256(goldenCase(t, c.sr, c.ch, c.bd, c.lvl))
		t.Logf("golden %d/%dch/%dbit/L%d = %s", c.sr, c.ch, c.bd, c.lvl, hex.EncodeToString(got[:]))
	}
}

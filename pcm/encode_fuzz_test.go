package pcm

import (
	"bytes"
	"testing"
)

// FuzzEncodeRoundTrip feeds arbitrary PCM and config selectors through the encoder
// and the decoder, asserting no panic and an exact round trip. The seekable sink
// makes the decoder verify the STREAMINFO MD5 too.
func FuzzEncodeRoundTrip(f *testing.F) {
	f.Add([]byte{0, 0, 1, 0, 2, 0, 3, 0, 0xFF, 0x7F}, uint8(1), uint8(1), uint8(5))
	f.Add(make([]byte, 600), uint8(0), uint8(0), uint8(0))
	f.Add(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC}, 100), uint8(2), uint8(0), uint8(8))

	f.Fuzz(func(t *testing.T, raw []byte, depthSel, chSel, levelSel uint8) {
		bitDepth := []int{8, 16, 24}[int(depthSel)%3]
		channels := int(chSel%2) + 1
		level := int(levelSel % 9)
		cfg := Config{SampleRate: 44100, BitDepth: bitDepth, Channels: channels, CompressionLevel: level}

		frameLen := ((bitDepth + 7) / 8) * channels
		raw = raw[:len(raw)-len(raw)%frameLen] // whole inter-channel samples only
		if len(raw) == 0 {
			return
		}

		var sink seekBuffer
		enc, err := NewEncoder(&sink, cfg)
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if _, err := enc.Write(raw); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		d, err := NewDecoder(bytes.NewReader(sink.Bytes()))
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		var out bytes.Buffer
		if _, err := d.WriteTo(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !bytes.Equal(out.Bytes(), raw) {
			t.Fatalf("round trip mismatch: in=%d out=%d", len(raw), out.Len())
		}
	})
}

package pcm

import (
	"bytes"
	"math"
	"testing"
)

// correlatedPCM builds interleaved little-endian 16-bit stereo PCM that LPC
// models well, so levels 3-8 must compress it better than level 2.
func correlatedPCM(frames int) []byte {
	buf := make([]byte, frames*2*2) // 2 channels, 2 bytes/sample
	var p1, p2 float64
	var state uint32 = 0x0BADF00D
	for i := range frames {
		state = state*1664525 + 1013904223
		noise := float64(int32(state>>9)%201) - 100
		v := 1.7*p1 - 0.72*p2 + noise
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		s := int16(math.Round(v))
		p2, p1 = p1, v
		off := i * 4
		buf[off] = byte(uint16(s))
		buf[off+1] = byte(uint16(s) >> 8)
		buf[off+2] = byte(uint16(s)) // duplicate to the right channel
		buf[off+3] = byte(uint16(s) >> 8)
	}
	return buf
}

func TestEncodeLPCRoundTripAndRatio(t *testing.T) {
	pcmData := correlatedPCM(50000)
	cfgBase := Config{SampleRate: 44100, BitDepth: 16, Channels: 2}

	sizes := map[int]int{}
	for _, level := range []int{2, 3, 5, 8} {
		cfg := cfgBase
		cfg.CompressionLevel = level

		sb := &seekBuffer{}
		enc, err := NewEncoder(sb, cfg)
		if err != nil {
			t.Fatalf("level %d: NewEncoder: %v", level, err)
		}
		if _, err := enc.Write(pcmData); err != nil {
			t.Fatalf("level %d: Write: %v", level, err)
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("level %d: Close: %v", level, err)
		}
		encoded := sb.Bytes()

		// Round trip + STREAMINFO MD5 check (decodeAll verifies MD5 at clean EOF).
		_, got := decodeAll(t, bytes.NewReader(encoded))
		if !bytes.Equal(got, pcmData) {
			t.Fatalf("level %d: PCM round-trip mismatch (got %d bytes, want %d)", level, len(got), len(pcmData))
		}
		sizes[level] = len(encoded)
	}

	// LPC levels must beat fixed-only level 2 on a correlated signal.
	for _, level := range []int{3, 5, 8} {
		if sizes[level] >= sizes[2] {
			t.Errorf("level %d size %d not smaller than level 2 size %d", level, sizes[level], sizes[2])
		}
	}
}

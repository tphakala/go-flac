package pcm

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/meta"
)

// streamInfoBlockSizes encodes nSamples mono 16-bit samples through the Encoder
// into a seekable buffer and returns the patched STREAMINFO min/max block sizes.
func streamInfoBlockSizes(t *testing.T, nSamples int) (minBlock, maxBlock int) {
	t.Helper()
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{SampleRate: 48000, Channels: 1, BitDepth: 16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(make([]byte, nSamples*2)); err != nil { // 1ch * 16-bit = 2 bytes/sample
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(sb.Bytes())))
	if err != nil {
		t.Fatalf("metadata after Close: %v", err)
	}
	return sm.MinBlock, sm.MaxBlock
}

// TestStreamInfoFixedBlockSizeExcludesShortFinalBlock pins the STREAMINFO
// contract for a fixed-blocksize stream: the minimum block size field excludes
// the last (possibly short) block, so minBlock == maxBlock == the nominal block
// size. When they differ, decoders treat the stream as variable-blocksize and
// every frame trips "sample or frame number does not increase correctly",
// marking the file non-seekable.
func TestStreamInfoFixedBlockSizeExcludesShortFinalBlock(t *testing.T) {
	cases := []struct {
		name     string
		nSamples int
		wantMin  int
		wantMax  int
	}{
		{"twoFullPlusShort", 2*encoderBlockSize + 100, encoderBlockSize, encoderBlockSize},
		{"oneFullPlusShort", encoderBlockSize + 1, encoderBlockSize, encoderBlockSize},
		{"exactMultiple", 3 * encoderBlockSize, encoderBlockSize, encoderBlockSize},
		{"oneFull", encoderBlockSize, encoderBlockSize, encoderBlockSize},
		// A stream shorter than one block is a single (last) block; min==max==its size.
		{"singleShort", 100, 100, 100},
		// Below the FLAC minimum block size, Close floors the field to the spec
		// minimum (16); the sole block is the last block, which is exempt from the
		// minimum, so the advertised value stays legal. 16 is pinned as a literal so a
		// regression lowering the constant below the RFC floor is caught.
		{"subMinimumFloored", 15, 16, 16},
		{"singleSampleFloored", 1, 16, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			minB, maxB := streamInfoBlockSizes(t, c.nSamples)
			// wantMin == wantMax for every case, so this one assertion also enforces
			// the MinBlock == MaxBlock invariant: a fixed-blocksize stream that
			// reports unequal bounds (short final block leaked into the minimum) is
			// treated as variable-blocksize and rejects go-flac's frame numbering.
			if minB != c.wantMin || maxB != c.wantMax {
				t.Errorf("MinBlock=%d MaxBlock=%d, want %d/%d (fixed-blocksize stream must report equal min/max, excluding the short final block)", minB, maxB, c.wantMin, c.wantMax)
			}
		})
	}
}

package frame

import (
	"bytes"
	"errors"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// randStereo16 builds one block of deterministic near-random 16-bit stereo PCM. The high
// entropy defeats compression, so the encoded frame is large (well over the 8 KiB reader
// block), which is what forces the decoder across a readMore refill.
func randStereo16(bs int, seed uint32) (left, right []int32) {
	left = make([]int32, bs)
	right = make([]int32, bs)
	s := seed
	nxt := func() int32 { s = s*1664525 + 1013904223; return int32(s>>16&0xFFFF) - 32768 } // full signed 16-bit
	for i := range left {
		left[i] = nxt()
		right[i] = nxt()
	}
	return left, right
}

// TestDecodeLargeFrameCrossesReaderRefill encodes one frame large enough to span several
// 8 KiB reader buffers, so decoding it drives the deferred CRC-16 record path through
// multiple readMore refills. The bulk CRC-16 (folded over the recorded scratch) must
// still match the stored frame CRC-16 across the refill boundaries, and every sample
// must round-trip. Sabotage: dropping the readMore deferred flush loses the pre-refill
// bytes, so sum16 no longer matches and Decode returns ErrCRCMismatch here.
func TestDecodeLargeFrameCrossesReaderRefill(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	const bs = 8192
	p := Params{Stereo: StereoFull, MaxPartitionOrder: 6, MaxLPCOrder: 8, LPCPrecision: 14, ExhaustiveFixed: true}
	l, r := randStereo16(bs, 0x9e3779b9)

	bw := bitio.NewWriter()
	enc := EncodeFrame(bw, NewWorkspace(bs, 2, 8), p, si, [][]int32{l, r}, 0)
	fb := bytes.Clone(enc) // copy: enc aliases bw's buffer
	if len(fb) <= 8192 {
		t.Fatalf("encoded frame is %d bytes; the test needs > 8192 to cross a reader refill", len(fb))
	}

	br := bitio.NewReader(bytes.NewReader(fb))
	var fr Frame
	if err := Decode(br, si, &fr); err != nil {
		t.Fatalf("decode large multi-refill frame: %v", err)
	}
	if fr.BlockSize != bs || len(fr.Channels) != 2 {
		t.Fatalf("bs=%d nch=%d", fr.BlockSize, len(fr.Channels))
	}
	for i := range bs {
		if fr.Channels[0][i] != l[i] || fr.Channels[1][i] != r[i] {
			t.Fatalf("sample %d: got (%d,%d) want (%d,%d)", i, fr.Channels[0][i], fr.Channels[1][i], l[i], r[i])
		}
	}
}

// TestDecodeLargeFrameCorruptPreRefillByteDetected proves the bulk CRC-16 covers a body
// byte that lives in the first reader block, i.e. a byte consumed and recorded before the
// first mid-frame refill compacts the buffer away. It has two halves that pin two distinct
// production lines. The positive control decodes the clean multi-refill frame: if a
// pre-refill run were dropped from the recorded scratch (a broken readMore flush or
// FlushTap), that clean decode would fail with a spurious CRC mismatch, so the control
// pins that pre-refill bytes ARE recorded. Then it flips one such byte and asserts the
// frame is rejected as a CRC mismatch, pinning that the stored-vs-computed CRC-16
// comparison actually runs and covers that byte. The fixture is deterministic (byte 4096
// is a residual byte, so decoding completes and the frame CRC-16 is what rejects it), so
// the mismatch is asserted hard rather than as a bare err != nil.
func TestDecodeLargeFrameCorruptPreRefillByteDetected(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	const bs = 8192
	p := Params{Stereo: StereoFull, MaxPartitionOrder: 6, MaxLPCOrder: 8, LPCPrecision: 14, ExhaustiveFixed: true}
	l, r := randStereo16(bs, 0x12345678)

	bw := bitio.NewWriter()
	enc := EncodeFrame(bw, NewWorkspace(bs, 2, 8), p, si, [][]int32{l, r}, 0)
	fb := bytes.Clone(enc)
	if len(fb) <= 8192 {
		t.Fatalf("encoded frame is %d bytes; the test needs > 8192 to place corruption before a refill", len(fb))
	}

	// Positive control: the clean multi-refill frame must decode. This fails if any
	// pre-refill run is missing from the recorded scratch, which is what gives the
	// corruption assertion below its pre-refill-coverage meaning.
	var fr Frame
	if err := Decode(bitio.NewReader(bytes.NewReader(fb)), si, &fr); err != nil {
		t.Fatalf("clean multi-refill frame failed to decode: %v", err)
	}

	// Flip a byte well inside the first 8 KiB block (past the header, before the refill)
	// on a copy, and confirm the frame CRC-16 rejects it.
	corrupt := bytes.Clone(fb)
	corrupt[4096] ^= 0xFF
	if err := Decode(bitio.NewReader(bytes.NewReader(corrupt)), si, &fr); !errors.Is(err, flac.ErrCRCMismatch) {
		t.Fatalf("corrupted pre-refill body byte: err = %v, want ErrCRCMismatch", err)
	}
}

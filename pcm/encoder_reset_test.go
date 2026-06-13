package pcm

import (
	"bytes"
	"io"
	"testing"
)

// writeAndClose feeds pcmBytes to enc in odd-sized chunks (to exercise the
// leftover/boundary path) and closes it, failing the test on any error.
func writeAndClose(t *testing.T, enc *Encoder, pcmBytes []byte) {
	t.Helper()
	for off := 0; off < len(pcmBytes); off += 777 {
		end := min(off+777, len(pcmBytes))
		if _, err := enc.Write(pcmBytes[off:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestEncoderResetRoundTrip encodes one stream, rebinds the same Encoder to a
// fresh sink with Reset, and encodes a second independent stream. Both streams
// must decode back to their exact inputs, and the Reset-produced stream must be
// byte-identical to one produced by a fresh NewEncoder with the same config:
// Reset must leak no per-stream state from the first encode.
func TestEncoderResetRoundTrip(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcm1 := genPCM(cfg, 4096*2+100)
	pcm2 := genPCM(cfg, 4096+513)

	sb1 := &seekBuffer{}
	enc, err := NewEncoder(sb1, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	writeAndClose(t, enc, pcm1)

	// Reset is called on a *closed* encoder: this is the pooling reuse pattern
	// (Get -> Reset -> Write -> Close -> Put repeated).
	sb2 := &seekBuffer{}
	if err := enc.Reset(sb2, cfg); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	writeAndClose(t, enc, pcm2)

	// Fresh encoder, same config and input as stream 2.
	sb3 := &seekBuffer{}
	enc3, err := NewEncoder(sb3, cfg)
	if err != nil {
		t.Fatalf("NewEncoder(3): %v", err)
	}
	writeAndClose(t, enc3, pcm2)

	if !bytes.Equal(sb2.Bytes(), sb3.Bytes()) {
		t.Fatalf("Reset stream not byte-identical to fresh encode (%d vs %d bytes)",
			len(sb2.Bytes()), len(sb3.Bytes()))
	}

	if _, got := decodeAll(t, bytes.NewReader(sb1.Bytes())); !bytes.Equal(got, pcm1) {
		t.Fatalf("stream 1 round trip mismatch (got %d bytes, want %d)", len(got), len(pcm1))
	}
	if _, got := decodeAll(t, bytes.NewReader(sb2.Bytes())); !bytes.Equal(got, pcm2) {
		t.Fatalf("stream 2 round trip mismatch (got %d bytes, want %d)", len(got), len(pcm2))
	}
}

// TestEncoderResetShapeChange resets across configs that change both the channel
// count and the LPC workspace shape (mono level 0 -> stereo 24-bit level 8),
// forcing the large buffers to be reallocated. Both streams must round-trip.
func TestEncoderResetShapeChange(t *testing.T) {
	cfgA := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0}
	cfgB := Config{SampleRate: 48000, BitDepth: 24, Channels: 2, CompressionLevel: 8}
	pcmA := genPCM(cfgA, 5000)
	pcmB := genPCM(cfgB, 5000)

	sbA := &seekBuffer{}
	enc, err := NewEncoder(sbA, cfgA)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	writeAndClose(t, enc, pcmA)

	sbB := &seekBuffer{}
	if err := enc.Reset(sbB, cfgB); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	writeAndClose(t, enc, pcmB)

	// After the shape change, the reallocated-buffer stream must still match a fresh
	// encode of the new shape byte for byte (catches header-only leaks across the
	// realloc path), as well as round-trip.
	sbFresh := &seekBuffer{}
	encFresh, err := NewEncoder(sbFresh, cfgB)
	if err != nil {
		t.Fatalf("NewEncoder(fresh B): %v", err)
	}
	writeAndClose(t, encFresh, pcmB)
	if !bytes.Equal(sbB.Bytes(), sbFresh.Bytes()) {
		t.Fatalf("shape-change Reset not byte-identical to fresh encode")
	}

	if _, got := decodeAll(t, bytes.NewReader(sbA.Bytes())); !bytes.Equal(got, pcmA) {
		t.Fatalf("stream A round trip mismatch")
	}
	if _, got := decodeAll(t, bytes.NewReader(sbB.Bytes())); !bytes.Equal(got, pcmB) {
		t.Fatalf("stream B round trip mismatch")
	}
}

// TestEncoderResetSeekTable resets into a seek-table config and back, verifying
// the seek-table placeholder/patch path is correctly re-initialized per stream.
func TestEncoderResetSeekTable(t *testing.T) {
	plain := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	seek := plain
	seek.SeekTableInterval = 4096 // a point roughly every block

	sb1 := &seekBuffer{}
	enc, err := NewEncoder(sb1, plain)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm1 := genPCM(plain, 4096*3+10)
	writeAndClose(t, enc, pcm1)

	sb2 := &seekBuffer{}
	if err := enc.Reset(sb2, seek); err != nil {
		t.Fatalf("Reset(seek): %v", err)
	}
	pcm2 := genPCM(seek, 4096*3+10)
	writeAndClose(t, enc, pcm2)

	// Byte-identical to a fresh encoder with the seek-table config.
	sb3 := &seekBuffer{}
	enc3, err := NewEncoder(sb3, seek)
	if err != nil {
		t.Fatalf("NewEncoder(3): %v", err)
	}
	writeAndClose(t, enc3, pcm2)
	if !bytes.Equal(sb2.Bytes(), sb3.Bytes()) {
		t.Fatalf("Reset seek-table stream not byte-identical to fresh encode")
	}

	if _, got := decodeAll(t, bytes.NewReader(sb2.Bytes())); !bytes.Equal(got, pcm2) {
		t.Fatalf("seek-table stream round trip mismatch")
	}
}

// TestEncoderResetSeekToPlain resets FROM a seek-table config back to a plain one
// and verifies all seek state is cleared. A leak here is invisible to the round
// trip but corrupts the bytes: a stale seekInterval would make Close patch a seek
// table into a stream that has no reserved SEEKTABLE block, so the byte-identical
// comparison against a fresh plain encode is the assertion that catches it.
func TestEncoderResetSeekToPlain(t *testing.T) {
	seek := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5, SeekTableInterval: 4096}
	plain := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}

	sb1 := &seekBuffer{}
	enc, err := NewEncoder(sb1, seek)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	writeAndClose(t, enc, genPCM(seek, 4096*3+10))

	sb2 := &seekBuffer{}
	if err := enc.Reset(sb2, plain); err != nil {
		t.Fatalf("Reset(plain): %v", err)
	}
	pcm2 := genPCM(plain, 4096*3+10)
	writeAndClose(t, enc, pcm2)

	sb3 := &seekBuffer{}
	enc3, err := NewEncoder(sb3, plain)
	if err != nil {
		t.Fatalf("NewEncoder(3): %v", err)
	}
	writeAndClose(t, enc3, pcm2)

	if !bytes.Equal(sb2.Bytes(), sb3.Bytes()) {
		t.Fatalf("seek->plain Reset not byte-identical to fresh plain encode (%d vs %d bytes)",
			len(sb2.Bytes()), len(sb3.Bytes()))
	}
	if _, got := decodeAll(t, bytes.NewReader(sb2.Bytes())); !bytes.Equal(got, pcm2) {
		t.Fatalf("seek->plain round trip mismatch")
	}
}

// TestEncoderResetDiscardsUnflushed exercises the documented contract that Reset
// drops input buffered from a previous, never-closed stream: a sub-block partial
// Write followed by Reset (no Close) must not bleed leftover bytes into the next
// stream's first block.
func TestEncoderResetDiscardsUnflushed(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}

	sb1 := &seekBuffer{}
	enc, err := NewEncoder(sb1, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	// Write less than one full block, then abandon the stream via Reset (no Close).
	partial := genPCM(cfg, 100)
	if _, err := enc.Write(partial); err != nil {
		t.Fatalf("Write(partial): %v", err)
	}

	sb2 := &seekBuffer{}
	if err := enc.Reset(sb2, cfg); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pcm2 := genPCM(cfg, 4096+513)
	writeAndClose(t, enc, pcm2)

	// The second stream must be byte-identical to a fresh encode of pcm2; any
	// surviving leftover from the abandoned stream would shift its first block.
	sb3 := &seekBuffer{}
	enc3, err := NewEncoder(sb3, cfg)
	if err != nil {
		t.Fatalf("NewEncoder(3): %v", err)
	}
	writeAndClose(t, enc3, pcm2)

	if !bytes.Equal(sb2.Bytes(), sb3.Bytes()) {
		t.Fatalf("Reset did not discard buffered input from the abandoned stream")
	}
	if _, got := decodeAll(t, bytes.NewReader(sb2.Bytes())); !bytes.Equal(got, pcm2) {
		t.Fatalf("post-Reset stream round trip mismatch")
	}
}

// TestEncoderResetValidation checks that Reset rejects the same invalid inputs as
// NewEncoder.
func TestEncoderResetValidation(t *testing.T) {
	good := Config{SampleRate: 44100, BitDepth: 16, Channels: 2}
	enc, err := NewEncoder(io.Discard, good)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	cases := []struct {
		name string
		w    io.Writer
		cfg  Config
	}{
		{"nil writer", nil, good},
		{"bad sample rate", io.Discard, Config{SampleRate: 0, BitDepth: 16, Channels: 2}},
		{"bad channels", io.Discard, Config{SampleRate: 44100, BitDepth: 16, Channels: 0}},
		{"bad bit depth", io.Discard, Config{SampleRate: 44100, BitDepth: 99, Channels: 2}},
		{"seektable on non-seeker", io.Discard, Config{SampleRate: 44100, BitDepth: 16, Channels: 2, SeekTableInterval: 44100}},
	}
	for _, tc := range cases {
		if err := enc.Reset(tc.w, tc.cfg); err == nil {
			t.Errorf("Reset(%s) = nil error, want error", tc.name)
		}
	}
}

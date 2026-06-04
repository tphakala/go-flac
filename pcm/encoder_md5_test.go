package pcm

import (
	"bytes"
	"crypto/md5"
	"errors"
	"testing"
)

// flipWriter is an in-memory sink that starts succeeding and can be flipped to
// fail every subsequent Write. It is used to drive emitBlock down its error
// path after a known number of frames have been accepted.
type flipWriter struct {
	buf  bytes.Buffer
	fail bool
}

var errFlipWrite = errors.New("flipWriter: forced write failure")

func (w *flipWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errFlipWrite
	}
	return w.buf.Write(p)
}

// TestEncoderMD5SkipsChunkOnWriteFailure verifies that emitBlock ingests a
// block into the STREAMINFO MD5 only after the sink accepts the frame. If the
// sink write fails, the block must not have been hashed, so a caller that
// retries the same input cannot double-ingest the PCM and silently corrupt the
// MD5. This is the behavior the md5/write reordering establishes.
func TestEncoderMD5SkipsChunkOnWriteFailure(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 5}
	w := &flipWriter{}
	enc, err := NewEncoder(w, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	blockBytes := encoderBlockSize * enc.frameLen
	block1 := genPCM(cfg, encoderBlockSize)
	block2 := genPCM(cfg, encoderBlockSize)
	if len(block1) != blockBytes || len(block2) != blockBytes {
		t.Fatalf("test setup: blocks must be exactly one full block (%d bytes)", blockBytes)
	}

	// First block goes through cleanly and is ingested into the MD5.
	if _, err := enc.Write(block1); err != nil {
		t.Fatalf("Write(block1): %v", err)
	}

	// Flip the sink to fail, then write the second full block: emitBlock encodes
	// the frame, the sink write fails, and Write returns the error.
	w.fail = true
	if _, err := enc.Write(block2); !errors.Is(err, errFlipWrite) {
		t.Fatalf("Write(block2) error = %v, want errFlipWrite", err)
	}

	// The MD5 must reflect block1 only: block2's chunk must not have been hashed.
	want := md5.Sum(block1)
	var got [16]byte
	copy(got[:], enc.md5.Sum(nil))
	if got != want {
		t.Fatalf("MD5 after failed write = %x, want %x (block2 was ingested despite the write failing?)", got, want)
	}
}

// TestEncoderMD5MatchesInputPCM confirms the success-path contract is unchanged:
// the finalized STREAMINFO MD5 equals md5 of the exact interleaved PCM fed in.
// This is the regression guard that the md5/write reordering stays byte-identical
// on the normal (no-error) path.
func TestEncoderMD5MatchesInputPCM(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096*2+1234) // two full frames + a short final frame

	var sink seekBuffer
	enc, err := NewEncoder(&sink, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for off := 0; off < len(pcmBytes); off += 777 {
		end := min(off+777, len(pcmBytes))
		if _, err := enc.Write(pcmBytes[off:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	si, _ := decodeAll(t, bytes.NewReader(sink.Bytes()))
	want := md5.Sum(pcmBytes)
	if si.MD5 != want {
		t.Fatalf("STREAMINFO MD5 = %x, want md5(input PCM) = %x", si.MD5, want)
	}
}

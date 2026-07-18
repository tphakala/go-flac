package pcm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/meta"
)

// streamInfoBlockSizesNonSeekable encodes nSamples mono 16-bit samples through the
// Encoder into a NON-seekable bytes.Buffer with Config.TotalSamples declared, and
// returns the STREAMINFO min/max block sizes. For this sink Close cannot seek back
// to patch the header, so the values it returns are the ones finalized up front by
// init. It mirrors streamInfoBlockSizes, which exercises the seekable path.
func streamInfoBlockSizesNonSeekable(t *testing.T, nSamples int) (minBlock, maxBlock int) {
	t.Helper()
	var buf bytes.Buffer // *bytes.Buffer is io.Writer but not io.WriteSeeker
	enc, err := NewEncoder(&buf, Config{SampleRate: 48000, Channels: 1, BitDepth: 16, TotalSamples: uint64(nSamples)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(make([]byte, nSamples*2)); err != nil { // 1ch * 16-bit = 2 bytes/sample
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatalf("metadata after Close: %v", err)
	}
	return sm.MinBlock, sm.MaxBlock
}

// TestStreamInfoNonSeekableFinalizesBlockSize is the regression guard for the
// BirdWeather soundscape corruption (birdnet-go #3965): a non-seekable encode
// with a declared TotalSamples must finalize STREAMINFO min/max block size up
// front, matching what the seekable path patches in Close, instead of leaving the
// 0 "unknown" sentinel. A decoder derives each fixed-blocksize frame's running
// sample number as frame_number * max_blocksize; a zero max_blocksize collapses
// every frame to sample 0, which trips libFLAC's "sample or frame number does not
// increase correctly" warning and makes strict decoders (Apple CoreAudio, browser
// Web Audio) reject the stream. The cases mirror the seekable-path test so the two
// sinks are pinned to identical expectations.
func TestStreamInfoNonSeekableFinalizesBlockSize(t *testing.T) {
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nonMin, nonMax := streamInfoBlockSizesNonSeekable(t, c.nSamples)
			if nonMin != c.wantMin || nonMax != c.wantMax {
				t.Errorf("non-seekable MinBlock=%d MaxBlock=%d, want %d/%d (must finalize block size up front, not leave the 0 sentinel)", nonMin, nonMax, c.wantMin, c.wantMax)
			}
			// Parity with the seekable path: a clip's seekability must not depend on
			// whether it was encoded to a file or an in-memory buffer.
			seekMin, seekMax := streamInfoBlockSizes(t, c.nSamples)
			if nonMin != seekMin || nonMax != seekMax {
				t.Errorf("non-seekable block sizes (%d/%d) disagree with seekable (%d/%d)", nonMin, nonMax, seekMin, seekMax)
			}
		})
	}
}

// TestEncodeInterleavedFinalizesBlockSize checks the one-shot buffer API finalizes
// STREAMINFO block size on a non-seekable sink too: it derives TotalSamples from
// the input length, so it flows through the same up-front finalization as a
// streaming encode with TotalSamples declared.
func TestEncodeInterleavedFinalizesBlockSize(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}
	const nSamples = 2*encoderBlockSize + 100
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, make([]byte, nSamples*2)); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if sm.MinBlock != encoderBlockSize || sm.MaxBlock != encoderBlockSize {
		t.Errorf("EncodeInterleaved MinBlock=%d MaxBlock=%d, want %d/%d", sm.MinBlock, sm.MaxBlock, encoderBlockSize, encoderBlockSize)
	}
}

// TestNonSeekableCrossValidateNoIncrementWarning is the end-to-end guard for
// birdnet-go #3965: it encodes to a NON-seekable buffer (the in-memory BirdWeather
// upload path) and asserts the reference flac tool does not emit the "sample or
// frame number does not increase correctly" warning that a zero max_blocksize
// triggers. `flac -t` exits 0 even when it prints that warning (the audio still
// decodes losslessly), so this inspects stderr text rather than only the exit code
// (which is why the exit-code-only cross-validation in encode_xvalidate_test.go
// would not have caught this). Skips when flac is unavailable.
func TestNonSeekableCrossValidateNoIncrementWarning(t *testing.T) {
	flacBin, err := exec.LookPath("flac")
	if err != nil {
		t.Skip("flac binary not found; skipping cross-validation")
	}
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1, CompressionLevel: 5}
	const nSamples = 5*encoderBlockSize + 777 // several full frames plus a short final frame
	pcmBytes := genPCM(cfg, nSamples)
	cfg.TotalSamples = nSamples

	var buf bytes.Buffer // *bytes.Buffer is io.Writer but not io.WriteSeeker
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcmBytes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(t.TempDir(), "nonseekable.flac")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write temp flac: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, flacBin, "-t", path) // not --silent: the warning goes to stderr
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		t.Fatalf("flac -t failed: %v: %s", runErr, stderr.String())
	}
	if strings.Contains(stderr.String(), "does not increase correctly") {
		t.Errorf("flac -t reported the frame-number increment warning on non-seekable output (max_blocksize not finalized):\n%s", stderr.String())
	}
}

// TestStreamInfoNonSeekableUnknownLengthAdvertisesBlockSize verifies that without a
// declared TotalSamples the non-seekable path advertises encoderBlockSize rather
// than the illegal 0 "unknown" sentinel. RFC 9639 requires the min/max block-size
// fields to be in 16..65535 and permits an advertised bound wider than the blocks
// actually written; encoderBlockSize is the only block size this encoder emits, and
// a shorter final block is exempt from the minimum, so it is valid for any length.
func TestStreamInfoNonSeekableUnknownLengthAdvertisesBlockSize(t *testing.T) {
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, Config{SampleRate: 48000, Channels: 1, BitDepth: 16}) // no TotalSamples
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(make([]byte, (2*encoderBlockSize+100)*2)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if sm.MinBlock != encoderBlockSize || sm.MaxBlock != encoderBlockSize {
		t.Errorf("MinBlock=%d MaxBlock=%d, want %d/%d for an undeclared length (never the illegal 0 sentinel)", sm.MinBlock, sm.MaxBlock, encoderBlockSize, encoderBlockSize)
	}
}

// TestStreamInfoNonSeekableClampsBelowMinimum verifies that a declared length below
// the FLAC minimum block size still yields a spec-legal min/max block size. RFC 9639
// forbids 0 and 1..15 in those fields, so a sub-16 declared total is floored to 16
// (the single short block is the last block, which is exempt from the minimum).
// Declared totals of 16 or more keep their exact single-block size.
func TestStreamInfoNonSeekableClampsBelowMinimum(t *testing.T) {
	cases := []struct {
		nSamples int
		want     int
	}{
		// The spec minimum is pinned as the literal 16, not minStreamInfoBlockSize, so
		// a regression that lowered the constant below the RFC-mandated floor would be
		// caught (expectation and code must not move in lockstep).
		{1, 16}, {15, 16}, {16, 16}, {17, 17}, {1000, 1000},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("n%d", c.nSamples), func(t *testing.T) {
			minB, maxB := streamInfoBlockSizesNonSeekable(t, c.nSamples)
			if minB != c.want || maxB != c.want {
				t.Errorf("n=%d: MinBlock=%d MaxBlock=%d, want %d/%d", c.nSamples, minB, maxB, c.want, c.want)
			}
		})
	}
}

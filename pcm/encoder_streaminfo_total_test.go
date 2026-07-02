package pcm

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestStreamInfoTotalNonSeekable verifies that a streaming encode to a plain
// (non-seekable) io.Writer with Config.TotalSamples set writes a finalized
// total_samples into STREAMINFO up front, while MD5 stays at the zero "absent"
// sentinel (the streaming path cannot hash before writing frames).
func TestStreamInfoTotalNonSeekable(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	const nSamples = 4096*2 + 1234
	pcmBytes := genPCM(cfg, nSamples)
	cfg.TotalSamples = nSamples

	var buf bytes.Buffer // *bytes.Buffer is not an io.Seeker
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

	si, got := decodeAll(t, bytes.NewReader(buf.Bytes()))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("round trip mismatch (got %d bytes, want %d)", len(got), len(pcmBytes))
	}
	if si.TotalSamples != nSamples {
		t.Errorf("TotalSamples = %d, want %d", si.TotalSamples, nSamples)
	}
	var zero [16]byte
	if si.MD5 != zero {
		t.Errorf("non-seekable streaming MD5 = %x, want zero sentinel", si.MD5)
	}
}

// TestStreamInfoTotalMismatch verifies that Close rejects a declared
// TotalSamples that does not match the number of samples actually written, so a
// caller cannot silently emit a wrong duration. The check runs before the
// seekable/non-seekable split in Close, so it must fire for both sink types.
func TestStreamInfoTotalMismatch(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 2}
	const nSamples = 1000
	pcmBytes := genPCM(cfg, nSamples)
	cfg.TotalSamples = nSamples + 1 // declare one sample too many

	sinks := map[string]func() io.Writer{
		"non-seekable": func() io.Writer { return &bytes.Buffer{} },
		"seekable":     func() io.Writer { return &seekBuffer{} },
	}
	for name, newSink := range sinks {
		t.Run(name, func(t *testing.T) {
			enc, err := NewEncoder(newSink(), cfg)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if _, err := enc.Write(pcmBytes); err != nil {
				t.Fatalf("Write: %v", err)
			}
			err = enc.Close()
			if err == nil {
				t.Fatal("Close with mismatched TotalSamples = nil, want error")
			}
			if !strings.Contains(err.Error(), "TotalSamples") {
				t.Errorf("Close error = %q, want it to mention TotalSamples", err)
			}
		})
	}
}

// TestStreamInfoTotalOverRange verifies that a declared TotalSamples larger than
// the FLAC 36-bit field is rejected at construction (rather than silently
// truncated by the bit writer), while the maximum legal value is accepted (guards
// an off-by-one in the bound).
func TestStreamInfoTotalOverRange(t *testing.T) {
	over := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, TotalSamples: 1 << 36}
	if _, err := NewEncoder(&bytes.Buffer{}, over); err == nil {
		t.Fatal("NewEncoder with TotalSamples 2^36 = nil, want error")
	}
	atMax := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, TotalSamples: 1<<36 - 1}
	if _, err := NewEncoder(&bytes.Buffer{}, atMax); err != nil {
		t.Fatalf("NewEncoder with TotalSamples 2^36-1 = %v, want nil", err)
	}
}

// TestStreamInfoTotalSeekableByteIdentical verifies that setting TotalSamples to
// the true count on a seekable sink leaves the output byte-for-byte unchanged
// (Close patches the actual total either way), i.e. the field is a no-op there
// beyond the up-front placeholder value that Close overwrites.
func TestStreamInfoTotalSeekableByteIdentical(t *testing.T) {
	base := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	const nSamples = 4096*2 + 1234
	pcmBytes := genPCM(base, nSamples)

	encode := func(cfg Config) []byte {
		var sb seekBuffer
		enc, err := NewEncoder(&sb, cfg)
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if _, err := enc.Write(pcmBytes); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return sb.Bytes()
	}

	withField := base
	withField.TotalSamples = nSamples
	if a, b := encode(base), encode(withField); !bytes.Equal(a, b) {
		t.Fatalf("seekable output differs when TotalSamples is set (%d vs %d bytes)", len(a), len(b))
	}

	withField.TotalSamples = nSamples
	si, _ := decodeAll(t, bytes.NewReader(encode(withField)))
	if si.TotalSamples != nSamples {
		t.Errorf("TotalSamples = %d, want %d", si.TotalSamples, nSamples)
	}
}

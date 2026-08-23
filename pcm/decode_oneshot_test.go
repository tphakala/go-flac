package pcm

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tphakala/go-flac"
)

// encodeFLAC returns a complete FLAC stream for the given PCM and config, using
// the one-shot encoder so the STREAMINFO totals are finalized.
func encodeFLAC(t *testing.T, cfg Config, pcm []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	return buf.Bytes()
}

// TestDecodeInterleavedRoundTrip checks the one-shot decode returns exactly the
// bytes the streaming decoder yields and populates the stream info.
func TestDecodeInterleavedRoundTrip(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096*2+321)
	stream := encodeFLAC(t, cfg, pcmBytes)

	got, info, err := DecodeInterleaved(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("DecodeInterleaved: %v", err)
	}
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(pcmBytes))
	}
	if info.SampleRate != cfg.SampleRate || info.Channels != cfg.Channels || info.BitDepth != cfg.BitDepth {
		t.Errorf("info = %+v, want rate/ch/depth %d/%d/%d", info, cfg.SampleRate, cfg.Channels, cfg.BitDepth)
	}

	// The one-shot must equal what the streaming decoder produces.
	_, want := decodeAll(t, bytes.NewReader(stream))
	if !bytes.Equal(got, want) {
		t.Fatal("DecodeInterleaved output differs from streaming decode")
	}
}

// TestDecodeInterleavedLimit covers the ceiling: a limit below the output size
// fails with ErrDecodeLimit and returns no partial buffer, a limit at exactly the
// output size succeeds, and a non-positive limit is unbounded.
func TestDecodeInterleavedLimit(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096*3)
	stream := encodeFLAC(t, cfg, pcmBytes)
	full := len(pcmBytes)

	t.Run("below output fails", func(t *testing.T) {
		got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), full-1)
		if !errors.Is(err, ErrDecodeLimit) {
			t.Fatalf("err = %v, want ErrDecodeLimit", err)
		}
		if got != nil {
			t.Errorf("got %d bytes back on limit error, want nil", len(got))
		}
	})

	t.Run("exact output succeeds", func(t *testing.T) {
		got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), full)
		if err != nil {
			t.Fatalf("DecodeInterleavedLimit(exact): %v", err)
		}
		if !bytes.Equal(got, pcmBytes) {
			t.Fatalf("exact-limit decode mismatch: got %d bytes, want %d", len(got), full)
		}
	})

	t.Run("non-positive is unbounded", func(t *testing.T) {
		for _, limit := range []int{0, -1} {
			got, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), limit)
			if err != nil {
				t.Fatalf("DecodeInterleavedLimit(%d): %v", limit, err)
			}
			if !bytes.Equal(got, pcmBytes) {
				t.Fatalf("unbounded decode mismatch (limit %d): got %d bytes, want %d", limit, len(got), full)
			}
		}
	})
}

// TestPresizeHint checks the up-front reservation is bounded: a declared length is
// never trusted past maxPreSize or the ceiling, and the ceil bytes-per-sample is
// used. This is the guard against a tiny file that declares a huge sample count
// driving a large allocation before any frame is decoded.
func TestPresizeHint(t *testing.T) {
	cases := []struct {
		name     string
		info     flac.StreamInfo
		maxBytes int
		want     int
	}{
		{"huge declared total is capped", flac.StreamInfo{TotalSamples: 1 << 34, Channels: 2, BitDepth: 16}, DefaultMaxDecodedBytes, maxPreSize},
		{"modest total reserved in full", flac.StreamInfo{TotalSamples: 44100, Channels: 2, BitDepth: 16}, DefaultMaxDecodedBytes, 44100 * 2 * 2},
		{"unknown total skips presize", flac.StreamInfo{TotalSamples: 0, Channels: 2, BitDepth: 16}, DefaultMaxDecodedBytes, 0},
		{"ceiling below cap bounds hint", flac.StreamInfo{TotalSamples: 1 << 20, Channels: 2, BitDepth: 16}, 1024, 1024},
		{"non-byte-aligned depth uses ceil", flac.StreamInfo{TotalSamples: 100, Channels: 1, BitDepth: 20}, DefaultMaxDecodedBytes, 100 * 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := presizeHint(c.info, c.maxBytes); got != c.want {
				t.Errorf("presizeHint(%+v, %d) = %d, want %d", c.info, c.maxBytes, got, c.want)
			}
		})
	}
}

// TestDecodeInterleavedBadStream checks a non-FLAC input is reported (not as a
// limit error) and never panics.
func TestDecodeInterleavedBadStream(t *testing.T) {
	got, _, err := DecodeInterleaved(bytes.NewReader([]byte("not a flac stream at all")))
	if err == nil {
		t.Fatal("DecodeInterleaved on garbage: want error, got nil")
	}
	if errors.Is(err, ErrDecodeLimit) {
		t.Errorf("garbage input reported as ErrDecodeLimit: %v", err)
	}
	if got != nil {
		t.Errorf("got %d bytes back on error, want nil", len(got))
	}
}

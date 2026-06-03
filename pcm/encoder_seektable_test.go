package pcm

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/meta"
)

func TestSeekTableRequiresWriteSeeker(t *testing.T) {
	// A bare bytes.Buffer is io.Writer but not io.WriteSeeker.
	_, err := NewEncoder(&bytes.Buffer{}, Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16, SeekTableInterval: 44100,
	})
	if err == nil {
		t.Fatal("expected error: seek table without io.WriteSeeker")
	}
}

func TestSeekTablePlaceholderWritten(t *testing.T) {
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16,
		SeekTableInterval: 44100, SeekTableMaxPoints: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = enc
	// Parse metadata: STREAMINFO (last=0), then a SEEKTABLE of 8 placeholder points,
	// then PADDING (last=1). The metadata must be well-formed even before Close.
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(sb.Bytes())))
	if err != nil {
		t.Fatalf("metadata not well-formed after NewEncoder: %v", err)
	}
	if len(sm.SeekPoints) != 0 {
		t.Fatalf("placeholder points should parse to 0 real points, got %d", len(sm.SeekPoints))
	}
	_ = flac.Version
}

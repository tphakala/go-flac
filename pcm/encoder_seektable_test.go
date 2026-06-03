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

func TestSeekTableFilledAndRepartitioned(t *testing.T) {
	var sb seekBuffer
	// interval = 4096 samples => a point at sample 0 and at each block boundary.
	enc, err := NewEncoder(&sb, Config{
		SampleRate: 44100, Channels: 2, BitDepth: 16,
		SeekTableInterval: 4096, SeekTableMaxPoints: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 3 full blocks => points for samples 0, 4096, 8192 (used = 3 < 64).
	data := make([]byte, 3*4096*2*2)
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(sb.Bytes())))
	if err != nil {
		t.Fatalf("metadata after Close: %v", err)
	}
	if len(sm.SeekPoints) != 3 {
		t.Fatalf("seek points = %d, want 3", len(sm.SeekPoints))
	}
	want := []uint64{0, 4096, 8192}
	for i, p := range sm.SeekPoints {
		if p.SampleNumber != want[i] {
			t.Fatalf("point[%d] sample = %d, want %d", i, p.SampleNumber, want[i])
		}
	}
	// The first point's byte offset is 0 (first frame), and offsets must be increasing.
	if sm.SeekPoints[0].ByteOffset != 0 {
		t.Fatalf("point[0] byte offset = %d, want 0", sm.SeekPoints[0].ByteOffset)
	}
	for i := 1; i < len(sm.SeekPoints); i++ {
		if sm.SeekPoints[i].ByteOffset <= sm.SeekPoints[i-1].ByteOffset {
			t.Fatalf("byte offsets not increasing at %d", i)
		}
	}
}

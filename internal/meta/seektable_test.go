package meta

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func TestParseSeekTableDropsPlaceholders(t *testing.T) {
	pts := []SeekPoint{
		{SampleNumber: 0, ByteOffset: 0, FrameSamples: 4096},
		{SampleNumber: 4096, ByteOffset: 5000, FrameSamples: 4096},
	}
	body := EncodeSeekPoints(pts)                             // 2 real points
	body = append(body, EncodeSeekPoints(placeholders(1))...) // + 1 placeholder
	got, err := parseSeekTable(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2 (placeholder dropped)", len(got))
	}
	if got[1].SampleNumber != 4096 || got[1].ByteOffset != 5000 {
		t.Fatalf("point[1] = %+v", got[1])
	}
}

func TestReadMetadataParsesSeekTable(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	var buf bytes.Buffer
	buf.Write([]byte("fLaC"))
	siBody := EncodeStreamInfo(si, 4096, 4096, 0, 0)
	buf.Write(EncodeBlockHeader(false, typeStreamInfo, len(siBody))) // last=0
	buf.Write(siBody)
	stBody := EncodeSeekPoints([]SeekPoint{{SampleNumber: 0, ByteOffset: 0, FrameSamples: 4096}})
	buf.Write(EncodeBlockHeader(true, TypeSeekTable, len(stBody))) // last=1
	buf.Write(stBody)

	sm, err := ReadMetadata(bitio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if len(sm.SeekPoints) != 1 || sm.SeekPoints[0].FrameSamples != 4096 {
		t.Fatalf("SeekPoints = %+v", sm.SeekPoints)
	}
}

func placeholders(n int) []SeekPoint {
	out := make([]SeekPoint, n)
	for i := range out {
		out[i] = SeekPoint{SampleNumber: PlaceholderSampleNumber}
	}
	return out
}

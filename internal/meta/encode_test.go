package meta

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func TestStreamInfoWriteRoundTrip(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16, TotalSamples: 123456}
	for i := range si.MD5 {
		si.MD5[i] = byte(i*7 + 1)
	}
	body := EncodeStreamInfo(si, 4096, 4096, 10, 5000)
	if len(body) != StreamInfoBodyLen {
		t.Fatalf("body len=%d, want %d", len(body), StreamInfoBodyLen)
	}
	var buf bytes.Buffer
	if err := WriteStreamHeader(&buf, body); err != nil {
		t.Fatal(err)
	}
	if StreamInfoBodyOffset != 8 {
		t.Fatalf("StreamInfoBodyOffset=%d, want 8", StreamInfoBodyOffset)
	}

	br := bitio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := ReadMetadata(br)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.Info.SampleRate != si.SampleRate || got.Info.Channels != si.Channels ||
		got.Info.BitDepth != si.BitDepth || got.Info.TotalSamples != si.TotalSamples {
		t.Fatalf("got %+v, want %+v", got.Info, si)
	}
	if got.Info.MD5 != si.MD5 {
		t.Fatalf("MD5 mismatch: got %x want %x", got.Info.MD5, si.MD5)
	}
}

func TestStreamInfoUnknownSentinels(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 48000, Channels: 1, BitDepth: 24} // total 0, MD5 zero
	body := EncodeStreamInfo(si, 0, 0, 0, 0)
	var buf bytes.Buffer
	if err := WriteStreamHeader(&buf, body); err != nil {
		t.Fatal(err)
	}
	br := bitio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := ReadMetadata(br)
	if err != nil {
		t.Fatal(err)
	}
	var zero [16]byte
	if got.Info.MD5 != zero || got.Info.TotalSamples != 0 {
		t.Fatalf("expected unknown sentinels, got MD5=%x total=%d", got.Info.MD5, got.Info.TotalSamples)
	}
}

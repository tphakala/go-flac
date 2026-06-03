package meta

import (
	"bytes"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// buildStreamInfoOnly returns "fLaC" + a last STREAMINFO block describing the args.
func buildStreamInfoOnly(minBlk, maxBlk, sampleRate, channels, bps int, total uint64, md5 [16]byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.WriteByte(0x80)                 // last-block flag set, block type 0 (STREAMINFO)
	buf.Write([]byte{0x00, 0x00, 0x22}) // length 34
	body := make([]byte, 0, 34)
	put16 := func(v int) { body = append(body, byte(v>>8), byte(v)) }
	put24 := func(v int) { body = append(body, byte(v>>16), byte(v>>8), byte(v)) }
	put16(minBlk)
	put16(maxBlk)
	put24(0) // min frame size (unknown)
	put24(0) // max frame size (unknown)
	// 20 bits sample rate, 3 bits (channels-1), 5 bits (bps-1), 36 bits total.
	var packed uint64
	packed = uint64(sampleRate) << 44
	packed |= uint64(channels-1) << 41
	packed |= uint64(bps-1) << 36
	packed |= total & 0xFFFFFFFFF
	for i := 7; i >= 0; i-- {
		body = append(body, byte(packed>>(uint(i)*8)))
	}
	body = append(body, md5[:]...)
	buf.Write(body)
	return buf.Bytes()
}

func TestReadStreamInfo(t *testing.T) {
	md5 := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	data := buildStreamInfoOnly(4096, 4096, 44100, 2, 16, 12345, md5)
	br := bitio.NewReader(bytes.NewReader(data))
	sm, err := ReadMetadata(br)
	if err != nil {
		t.Fatal(err)
	}
	if sm.Info.SampleRate != 44100 || sm.Info.Channels != 2 || sm.Info.BitDepth != 16 {
		t.Fatalf("got %+v", sm.Info)
	}
	if sm.Info.TotalSamples != 12345 || sm.Info.MD5 != md5 {
		t.Fatalf("got total=%d md5=%x", sm.Info.TotalSamples, sm.Info.MD5)
	}
}

func TestSkipsLeadingID3v2(t *testing.T) {
	md5 := [16]byte{}
	core := buildStreamInfoOnly(4096, 4096, 48000, 1, 24, 0, md5)
	// ID3v2 header: "ID3", ver 0x0400, flags 0, syncsafe size = 10 bytes of body.
	id3 := make([]byte, 0, 20+len(core))
	id3 = append(id3, 'I', 'D', '3', 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0A)
	id3 = append(id3, make([]byte, 10)...)
	id3 = append(id3, core...)
	data := id3
	br := bitio.NewReader(bytes.NewReader(data))
	sm, err := ReadMetadata(br)
	if err != nil {
		t.Fatal(err)
	}
	if sm.Info.SampleRate != 48000 || sm.Info.Channels != 1 || sm.Info.BitDepth != 24 {
		t.Fatalf("got %+v", sm.Info)
	}
}

func TestSkipsID3v2WithFooter(t *testing.T) {
	core := buildStreamInfoOnly(4096, 4096, 44100, 2, 16, 0, [16]byte{})
	// Flags byte 0x10 marks a 10-byte footer not counted in the syncsafe size.
	id3 := make([]byte, 0, 30+len(core))
	id3 = append(id3, 'I', 'D', '3', 0x04, 0x00, 0x10, 0x00, 0x00, 0x00, 0x0A)
	id3 = append(id3, make([]byte, 10)...) // tag body (size = 10)
	id3 = append(id3, make([]byte, 10)...) // footer (skipped via the footer flag)
	id3 = append(id3, core...)
	br := bitio.NewReader(bytes.NewReader(id3))
	sm, err := ReadMetadata(br)
	if err != nil {
		t.Fatal(err)
	}
	if sm.Info.SampleRate != 44100 || sm.Info.Channels != 2 || sm.Info.BitDepth != 16 {
		t.Fatalf("got %+v", sm.Info)
	}
}

func TestTruncatedMetadataIsUnexpectedEOF(t *testing.T) {
	// A bare stream marker with no metadata blocks is truncated, not a clean end.
	br := bitio.NewReader(bytes.NewReader([]byte("fLaC")))
	if _, err := ReadMetadata(br); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestMissingStreamInfo(t *testing.T) {
	// "fLaC" then a PADDING block (type 1) marked last: STREAMINFO never appears.
	data := []byte("fLaC")
	data = append(data, 0x81, 0x00, 0x00, 0x04, 0, 0, 0, 0) // last, type 1, len 4
	br := bitio.NewReader(bytes.NewReader(data))
	if _, err := ReadMetadata(br); !errors.Is(err, flac.ErrMissingStreamInfo) {
		t.Fatalf("want ErrMissingStreamInfo, got %v", err)
	}
}

func TestBadMagic(t *testing.T) {
	br := bitio.NewReader(bytes.NewReader([]byte("OggS....")))
	if _, err := ReadMetadata(br); err == nil {
		t.Fatal("want error on bad magic")
	}
}

func TestReadMetadataCapturesSizes(t *testing.T) {
	// A minimal stream: "fLaC" + STREAMINFO(last) with min=max block=4096,
	// min frame=10, max frame=20, 44100 Hz, 2 ch, 16 bps, 0 samples, zero MD5.
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	body := EncodeStreamInfo(si, 4096, 4096, 10, 20)
	var buf bytes.Buffer
	if err := WriteStreamHeader(&buf, body); err != nil {
		t.Fatal(err)
	}
	sm, err := ReadMetadata(bitio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if sm.Info.SampleRate != 44100 || sm.Info.Channels != 2 || sm.Info.BitDepth != 16 {
		t.Fatalf("StreamInfo wrong: %+v", sm.Info)
	}
	if sm.MinBlock != 4096 || sm.MaxBlock != 4096 || sm.MaxFrame != 20 {
		t.Fatalf("sizes wrong: min=%d max=%d maxFrame=%d", sm.MinBlock, sm.MaxBlock, sm.MaxFrame)
	}
	if sm.SeekPoints != nil {
		t.Fatalf("SeekPoints should be nil for a stream with no SEEKTABLE, got %v", sm.SeekPoints)
	}
}

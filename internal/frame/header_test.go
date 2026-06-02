package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// buildFrameHeader builds a fixed-blocksize header with blocksize=4096 (code 0xC),
// sample rate from STREAMINFO (code 0), channel assignment 1 (2 independent ch),
// bps from STREAMINFO (code 0), frame number 0.
func buildFrameHeader(frameNumber byte) []byte {
	// 5 header bytes + 1 CRC-8 byte.
	body := make([]byte, 5, 6)
	body[0] = 0xFF // sync 0x3FFE (14b) + reserved 0 + blocking strategy 0 (fixed)
	body[1] = 0xF8
	body[2] = 0xC0        // blocksize code 0b1100 (4096), sample rate code 0b0000 (streaminfo)
	body[3] = 0x10        // channel assignment 0b0001 (2 ch), bps code 0b000 (streaminfo), reserved 0
	body[4] = frameNumber // UTF-8 coded number (single byte < 0x80)
	return append(body, crc.Checksum8(body))
}

func TestReadHeaderFixedBlocksize(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	data := buildFrameHeader(0)
	br := bitio.NewReader(bytes.NewReader(data))
	var hdr header
	if err := readHeader(br, si, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.blockSize != 4096 {
		t.Fatalf("blockSize=%d want 4096", hdr.blockSize)
	}
	if hdr.sampleRate != 44100 || hdr.bitsPerSample != 16 {
		t.Fatalf("rate=%d bps=%d", hdr.sampleRate, hdr.bitsPerSample)
	}
	if hdr.channelAssignment != 1 || hdr.channels() != 2 {
		t.Fatalf("chAssign=%d ch=%d", hdr.channelAssignment, hdr.channels())
	}
}

func TestReadHeaderBadCRC(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	data := buildFrameHeader(0)
	data[len(data)-1] ^= 0xFF // corrupt CRC-8
	br := bitio.NewReader(bytes.NewReader(data))
	var hdr header
	if err := readHeader(br, si, &hdr); err == nil {
		t.Fatal("want CRC error")
	}
}

func TestReadHeaderBadSync(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	data := buildFrameHeader(0)
	data[0] = 0x00
	br := bitio.NewReader(bytes.NewReader(data))
	var hdr header
	if err := readHeader(br, si, &hdr); err == nil {
		t.Fatal("want sync error")
	}
}

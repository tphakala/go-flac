package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func TestWriteFrameHeaderRoundTrip(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	cases := []struct {
		bs, chCode int
		num        uint64
	}{
		{4096, 1, 0},       // independent stereo, frame 0
		{4096, 8, 1},       // left/side, frame 1 (2-byte UTF-8)
		{1234, 10, 200},    // mid/side, short final frame (bsCode 7, 16-bit ext)
		{192, 0, 0x1FF},    // mono, direct blocksize code 1, 2-byte UTF-8 number
		{4096, 0, 1 << 30}, // large frame number (multi-byte UTF-8)
	}
	for _, tc := range cases {
		bw := bitio.NewWriter()
		writeFrameHeader(bw, tc.bs, tc.chCode, tc.num)
		bw.AlignByte()
		var hdr header
		br := bitio.NewReader(bytes.NewReader(bw.Bytes()))
		if err := readHeader(br, si, &hdr); err != nil {
			t.Fatalf("bs=%d ch=%d num=%d: readHeader: %v", tc.bs, tc.chCode, tc.num, err)
		}
		if hdr.blockSize != tc.bs {
			t.Errorf("bs=%d ch=%d num=%d: blockSize=%d, want %d", tc.bs, tc.chCode, tc.num, hdr.blockSize, tc.bs)
		}
		if hdr.channelAssignment != tc.chCode {
			t.Errorf("bs=%d ch=%d num=%d: chCode=%d, want %d", tc.bs, tc.chCode, tc.num, hdr.channelAssignment, tc.chCode)
		}
		if hdr.number != tc.num {
			t.Errorf("bs=%d ch=%d num=%d: number=%d, want %d", tc.bs, tc.chCode, tc.num, hdr.number, tc.num)
		}
		if hdr.sampleRate != 44100 || hdr.bitsPerSample != 16 {
			t.Errorf("bs=%d ch=%d num=%d: rate=%d bps=%d, want 44100/16", tc.bs, tc.chCode, tc.num, hdr.sampleRate, hdr.bitsPerSample)
		}
	}
}

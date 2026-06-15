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
		writeFrameHeader(bw, tc.bs, tc.chCode, si.SampleRate, si.BitDepth, tc.num)
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

// TestWriteFrameHeaderExplicitCodes verifies the frame header carries the sample
// rate and bit depth as explicit codes rather than the "read from STREAMINFO"
// code 0, so strict decoders such as Apple CoreAudio (iOS/macOS Safari) accept
// the stream. The header is decoded against a STREAMINFO whose rate/depth differ
// from what was encoded, so a correct round trip can only come from explicit
// frame-header codes; values with no dedicated code fall back to code 0 and are
// then resolved from STREAMINFO. Regression for go-flac issue #33.
func TestWriteFrameHeaderExplicitCodes(t *testing.T) {
	// Deliberately wrong STREAMINFO: any value recovered from it (rate 1, depth 8)
	// signals a code-0 fallback rather than an explicit frame-header code.
	bogus := flac.StreamInfo{SampleRate: 1, Channels: 1, BitDepth: 8}
	cases := []struct {
		name                 string
		bs                   int
		sampleRate, bitDepth int
		wantSR, wantBPS      int
	}{
		{"std-48k-16", 4096, 48000, 16, 48000, 16},
		{"std-8k-8", 4096, 8000, 8, 8000, 8},
		{"std-16k-12", 4096, 16000, 12, 16000, 12},
		{"std-44k1-20", 4096, 44100, 20, 44100, 20},
		{"std-96k-24", 4096, 96000, 24, 96000, 24},
		{"std-192k-32", 4096, 192000, 32, 192000, 32},
		{"escape-kHz-100000", 4096, 100000, 16, 100000, 16}, // code 12 (8-bit kHz)
		{"escape-Hz-11025", 4096, 11025, 16, 11025, 16},     // code 13 (16-bit Hz)
		{"escape-tens-96010", 4096, 96010, 16, 96010, 16},   // code 14 (16-bit tens of Hz)
		{"both-ext-1234-11025", 1234, 11025, 16, 11025, 16}, // blocksize ext + sample-rate ext together
		{"fallback-rate", 4096, 123457, 16, 1, 16},          // rate code 0 -> STREAMINFO rate
		{"fallback-depth", 4096, 48000, 14, 48000, 8},       // bps code 0 -> STREAMINFO depth
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bw := bitio.NewWriter()
			writeFrameHeader(bw, tc.bs, 0, tc.sampleRate, tc.bitDepth, 0)
			bw.AlignByte()
			var hdr header
			br := bitio.NewReader(bytes.NewReader(bw.Bytes()))
			if err := readHeader(br, bogus, &hdr); err != nil {
				t.Fatalf("readHeader: %v", err)
			}
			if hdr.blockSize != tc.bs {
				t.Errorf("blockSize = %d, want %d", hdr.blockSize, tc.bs)
			}
			if hdr.sampleRate != tc.wantSR {
				t.Errorf("sampleRate = %d, want %d", hdr.sampleRate, tc.wantSR)
			}
			if hdr.bitsPerSample != tc.wantBPS {
				t.Errorf("bitsPerSample = %d, want %d", hdr.bitsPerSample, tc.wantBPS)
			}
		})
	}
}

// TestFrameHeaderCodeTables locks the code chosen for each representable sample
// rate and bit depth, and the fallback to code 0 for values with no code.
func TestFrameHeaderCodeTables(t *testing.T) {
	srCases := []struct {
		sr, code, extN, extV int
	}{
		{8000, 4, 0, 0}, {16000, 5, 0, 0}, {22050, 6, 0, 0}, {24000, 7, 0, 0},
		{32000, 8, 0, 0}, {44100, 9, 0, 0}, {48000, 10, 0, 0}, {88200, 1, 0, 0},
		{96000, 11, 0, 0}, {176400, 2, 0, 0}, {192000, 3, 0, 0},
		{100000, 12, 8, 100},   // kHz escape
		{11025, 13, 16, 11025}, // Hz escape
		{96010, 14, 16, 9601},  // tens-of-Hz escape
		{123457, 0, 0, 0},      // no representation -> STREAMINFO
	}
	for _, c := range srCases {
		code, extN, extV := sampleRateCode(c.sr)
		if code != c.code || extN != c.extN || extV != c.extV {
			t.Errorf("sampleRateCode(%d) = (%d,%d,%d), want (%d,%d,%d)",
				c.sr, code, extN, extV, c.code, c.extN, c.extV)
		}
	}
	bpsCases := []struct{ bps, code int }{
		{8, 1}, {12, 2}, {16, 4}, {20, 5}, {24, 6}, {32, 7}, {14, 0}, {0, 0},
	}
	for _, c := range bpsCases {
		if got := bitDepthCode(c.bps); got != c.code {
			t.Errorf("bitDepthCode(%d) = %d, want %d", c.bps, got, c.code)
		}
	}
}

// TestSampleRateCodePanicsOnInvalid documents the precondition: the sample rate is
// validated at pcm.NewEncoder, so a non-positive value reaching sampleRateCode is a
// programming error and must fail fast rather than silently fall back to code 0.
func TestSampleRateCodePanicsOnInvalid(t *testing.T) {
	for _, sr := range []int{0, -1, -48000} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("sampleRateCode(%d) did not panic", sr)
				}
			}()
			sampleRateCode(sr)
		}()
	}
}

package frame

import (
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// Round-trip a wide independent (mono) frame end to end through EncodeFrame and Decode.
// This fails before the dispatch fix because Decode uses int32 for independent channels.
func TestDecodeWideMonoRoundTrip(t *testing.T) {
	for _, bps := range []int{25, 28, 32} {
		bs := 1024
		mono := asInt32(wideSamples(bs, bps, int64(bps)))
		p := paramsLevel(t, 5)
		si := flac.StreamInfo{SampleRate: 48000, Channels: 1, BitDepth: bps}

		bw := bitio.NewWriter()
		data := EncodeFrame(bw, p, si, [][]int32{mono}, 0)

		var fr Frame
		br := bitio.NewReader(bytesReaderFrame(data))
		if err := Decode(br, si, &fr); err != nil {
			t.Fatalf("bps %d Decode: %v", bps, err)
		}
		for i := range bs {
			if fr.Channels[0][i] != mono[i] {
				t.Fatalf("bps %d sample[%d] = %d, want %d", bps, i, fr.Channels[0][i], mono[i])
			}
		}
	}
}

// Round-trip wide stereo at 25-31 bps (the band the bps==32 gate missed).
func TestDecodeWideStereoRoundTrip(t *testing.T) {
	for _, bps := range []int{25, 28, 31} {
		bs := 2048
		l := asInt32(wideSamples(bs, bps, 1))
		r := asInt32(wideSamples(bs, bps, 2))
		p := paramsLevel(t, 8)
		si := flac.StreamInfo{SampleRate: 96000, Channels: 2, BitDepth: bps}

		bw := bitio.NewWriter()
		data := EncodeFrame(bw, p, si, [][]int32{l, r}, 0)

		var fr Frame
		br := bitio.NewReader(bytesReaderFrame(data))
		if err := Decode(br, si, &fr); err != nil {
			t.Fatalf("bps %d Decode: %v", bps, err)
		}
		for i := range bs {
			if fr.Channels[0][i] != l[i] || fr.Channels[1][i] != r[i] {
				t.Fatalf("bps %d sample[%d] = (%d,%d), want (%d,%d)", bps, i, fr.Channels[0][i], fr.Channels[1][i], l[i], r[i])
			}
		}
	}
}

package frame

import (
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// Round-trip a wide independent (mono) frame end to end through EncodeFrame and Decode.
// Exercises the int64 independent-channel decode dispatch for bps >= 25.
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

// A stream that decodes an independent wide stereo frame (grows work64[0] only) and
// then a decorrelated wide stereo frame into the SAME Frame must not panic: the
// decorrelated path must size work64[1] even when work64[0] is already large enough.
// Regression test for the work64[0]/work64[1] capacity desync.
func TestDecodeWideMixedAssignmentReuseFrame(t *testing.T) {
	const bps = 32
	bs := 512
	si := flac.StreamInfo{SampleRate: 96000, Channels: 2, BitDepth: bps}
	var fr Frame

	// Frame A: independent wide stereo. paramsLevel(t, 0) uses StereoIndependent, so
	// EncodeFrame takes the wide independent path and writes channel assignment 1,
	// which Decode handles via the independent int64 path (grows work64[0] only).
	la := asInt32(wideSamples(bs, bps, 1))
	ra := asInt32(wideSamples(bs, bps, 2)) // uncorrelated
	bwA := bitio.NewWriter()
	dataA := EncodeFrame(bwA, paramsLevel(t, 0), si, [][]int32{la, ra}, 0)
	if err := Decode(bitio.NewReader(bytesReaderFrame(dataA)), si, &fr); err != nil {
		t.Fatalf("frame A decode: %v", err)
	}
	for i := range bs {
		if fr.Channels[0][i] != la[i] || fr.Channels[1][i] != ra[i] {
			t.Fatalf("frame A sample[%d] = (%d,%d), want (%d,%d)", i, fr.Channels[0][i], fr.Channels[1][i], la[i], ra[i])
		}
	}

	// Frame B: decorrelated wide stereo (correlated channels so a mid/side or
	// left/right-side assignment is chosen), reusing fr. Under the bug this panics
	// because work64[1] is still cap 0.
	lb := asInt32(wideSamples(bs, bps, 3))
	rb := make([]int32, bs)
	for i := range rb {
		rb[i] = lb[i] - int32(i%5) + 2
	}
	bwB := bitio.NewWriter()
	dataB := EncodeFrame(bwB, paramsLevel(t, 8), si, [][]int32{lb, rb}, 1)
	if err := Decode(bitio.NewReader(bytesReaderFrame(dataB)), si, &fr); err != nil {
		t.Fatalf("frame B decode: %v", err)
	}
	for i := range bs {
		if fr.Channels[0][i] != lb[i] || fr.Channels[1][i] != rb[i] {
			t.Fatalf("frame B sample[%d] = (%d,%d), want (%d,%d)", i, fr.Channels[0][i], fr.Channels[1][i], lb[i], rb[i])
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

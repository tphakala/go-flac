package frame

import (
	"bytes"
	"slices"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func roundTripFrame(t *testing.T, si flac.StreamInfo, p Params, ch [][]int32, num uint64) {
	t.Helper()
	bw := bitio.NewWriter()
	buf := EncodeFrame(bw, NewWorkspace(len(ch[0]), len(ch), 12), p, si, ch, num)

	var dst Frame
	br := bitio.NewReader(bytes.NewReader(buf))
	if err := Decode(br, si, &dst); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dst.BlockSize != len(ch[0]) {
		t.Fatalf("blockSize=%d, want %d", dst.BlockSize, len(ch[0]))
	}
	if len(dst.Channels) != len(ch) {
		t.Fatalf("channels=%d, want %d", len(dst.Channels), len(ch))
	}
	for c := range ch {
		for i := range ch[c] {
			if dst.Channels[c][i] != ch[c][i] {
				t.Fatalf("ch %d sample %d: got %d, want %d", c, i, dst.Channels[c][i], ch[c][i])
			}
		}
	}
	if dst.Number != num {
		t.Fatalf("number=%d, want %d", dst.Number, num)
	}
}

func TestEncodeFrameRoundTrip(t *testing.T) {
	const bs = 4096
	mk := func(f func(i int) int32, n int) []int32 {
		s := make([]int32, n)
		for i := range s {
			s[i] = f(i)
		}
		return s
	}

	// mono 16-bit
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Params{Stereo: StereoIndependent, MaxPartitionOrder: 4},
		[][]int32{mk(func(i int) int32 { return int32(i%500 - 250) }, bs)}, 0)

	// stereo, correlated channels (favors mid/side) -> full search
	l := mk(func(i int) int32 { return int32(i%1000 - 500) }, bs)
	r := mk(func(i int) int32 { return int32(i%1000-500) + int32(i%5) }, bs)
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16},
		Params{Stereo: StereoFull, MaxPartitionOrder: 5}, [][]int32{l, r}, 1)

	// stereo, identical channels (side == 0) -> a side mode should win, must round-trip
	same := mk(func(i int) int32 { return int32(i%321 - 160) }, bs)
	roundTripFrame(t, flac.StreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 16},
		Params{Stereo: StereoFull, MaxPartitionOrder: 4}, [][]int32{same, slices.Clone(same)}, 2)

	// stereo independent forced
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16},
		Params{Stereo: StereoIndependent, MaxPartitionOrder: 4}, [][]int32{l, r}, 3)

	// adaptive mid-side
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16},
		Params{Stereo: StereoAdaptive, MaxPartitionOrder: 3}, [][]int32{l, r}, 4)

	// short final frame (non-table block size)
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16},
		Params{Stereo: StereoFull, MaxPartitionOrder: 4},
		[][]int32{l[:1234], r[:1234]}, 5)

	// 24-bit stereo
	l24 := mk(func(i int) int32 { return int32(i%100000 - 50000) }, bs)
	r24 := mk(func(i int) int32 { return int32(i%100000-50000) + int32(i%7) }, bs)
	roundTripFrame(t, flac.StreamInfo{SampleRate: 96000, Channels: 2, BitDepth: 24},
		Params{Stereo: StereoFull, MaxPartitionOrder: 6}, [][]int32{l24, r24}, 6)

	// 4-channel independent
	roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 4, BitDepth: 16},
		Params{Stereo: StereoFull, MaxPartitionOrder: 4},
		[][]int32{mk(func(i int) int32 { return int32(i % 100) }, bs),
			mk(func(i int) int32 { return int32(i%200 - 100) }, bs),
			mk(func(i int) int32 { return int32(-i % 150) }, bs),
			mk(func(i int) int32 { return int32(i%77 - 38) }, bs)}, 7)
}

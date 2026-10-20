package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func FuzzDecode(f *testing.F) {
	f.Add(buildOneFrame())
	// The out-of-range fixtures steer the fuzzer toward the range check.
	for _, c := range rangeCases() {
		f.Add(c.bytes())
	}
	// The oversized-residual fixtures steer it toward long Rice quotients.
	for _, c := range residualRangeCases() {
		f.Add(c.bytes())
	}
	// A frame header has no code for 25 to 31 bits, so such a frame takes its depth
	// from STREAMINFO. Decoding each input at 16 and at 28 bits lets the wide
	// fixtures (and mutations of them) reach the int64 path.
	infos := [...]flac.StreamInfo{
		{SampleRate: 44100, Channels: 2, BitDepth: 16},
		{SampleRate: 44100, Channels: 2, BitDepth: 28},
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, si := range infos {
			checkFuzzFrame(t, data, si)
		}
	})
}

// checkFuzzFrame decodes data as one frame under si and checks that an accepted
// frame holds only samples that fit its bit depth.
func checkFuzzFrame(t *testing.T, data []byte, si flac.StreamInfo) {
	t.Helper()
	br := bitio.NewReader(bytes.NewReader(data))
	var fr Frame
	if err := Decode(br, si, &fr); err != nil { // must never panic
		return
	}
	// An accepted frame holds only samples that fit its bit depth, whichever
	// subframe types and arithmetic routes produced them. A frame at 32 bits
	// fills int32, so there is nothing to compare.
	if fr.BitsPerSample >= 32 {
		return
	}
	lim := int32(1) << (fr.BitsPerSample - 1)
	for ch, samples := range fr.Channels {
		for i, v := range samples[:fr.BlockSize] {
			if v < -lim || v >= lim {
				t.Fatalf("accepted frame at %d-bit STREAMINFO: channel %d sample %d = %d outside %d bits", si.BitDepth, ch, i, v, fr.BitsPerSample)
			}
		}
	}
}

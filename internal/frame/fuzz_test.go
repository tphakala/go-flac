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
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	f.Fuzz(func(t *testing.T, data []byte) {
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
					t.Fatalf("accepted frame: channel %d sample %d = %d outside %d bits", ch, i, v, fr.BitsPerSample)
				}
			}
		}
	})
}

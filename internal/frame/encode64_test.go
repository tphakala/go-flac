package frame

import (
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// encodeStereo64 must produce a frame that frame.Decode reconstructs exactly at 32 bps.
// frame.Decode already dispatches int64 for 32-bps stereo, so this test does not depend
// on Task 9. The channels are strongly correlated so a side-using mode (assignment >= 8)
// is chosen; that keeps decode on the int64 stereo path rather than the int32 independent
// path (only fixed in Task 9).
func TestEncodeStereo64RoundTrip32(t *testing.T) {
	const bps = 32
	bs := 2048
	l64 := wideSamples(bs, bps, 11)
	r64 := make([]int64, bs)
	for i := range r64 {
		r64[i] = l64[i] - int64(i%7) + 3
	}
	l, r := asInt32(l64), asInt32(r64)

	p := paramsLevel(t, 8)
	si := flac.StreamInfo{SampleRate: 96000, Channels: 2, BitDepth: bps}

	bw := bitio.NewWriter()
	bw.Reset()
	encodeStereo64(bw, p, bps, bs, l, r, 0)
	data := bw.Bytes()

	var fr Frame
	br := bitio.NewReader(bytesReaderFrame(data))
	if err := Decode(br, si, &fr); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i := range bs {
		if fr.Channels[0][i] != l[i] || fr.Channels[1][i] != r[i] {
			t.Fatalf("sample[%d] = (%d,%d), want (%d,%d)", i, fr.Channels[0][i], fr.Channels[1][i], l[i], r[i])
		}
	}
}

package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

func FuzzDecode(f *testing.F) {
	f.Add(buildOneFrame())
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	f.Fuzz(func(t *testing.T, data []byte) {
		br := bitio.NewReader(bytes.NewReader(data))
		var fr Frame
		_ = Decode(br, si, &fr) // must never panic
	})
}

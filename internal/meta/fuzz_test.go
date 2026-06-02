package meta

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

func FuzzReadMetadata(f *testing.F) {
	f.Add([]byte("fLaC"))
	f.Add(buildStreamInfoOnly(4096, 4096, 44100, 2, 16, 0, [16]byte{}))
	f.Fuzz(func(t *testing.T, data []byte) {
		br := bitio.NewReader(bytes.NewReader(data))
		_, _ = ReadMetadata(br) // must never panic
	})
}

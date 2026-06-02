package bitio

import (
	"bytes"
	"testing"
)

func FuzzReadBits(f *testing.F) {
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xAB, 0x12, 0x34})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		// Drain in mixed widths; must never panic, only error at EOF.
		for {
			if _, err := r.ReadBits(7); err != nil {
				break
			}
			if _, err := r.ReadUnary(); err != nil {
				break
			}
			if _, err := r.ReadSigned(13); err != nil {
				break
			}
		}
	})
}

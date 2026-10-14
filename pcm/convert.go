package pcm

import "github.com/tphakala/go-flac/internal/frame"

// shiftSamples rescales every decoded sample of fr in place by shift bits: left for
// shift > 0 (widening, exact), arithmetic right for shift < 0 (narrowing, truncating
// toward negative infinity). shift != 0.
func shiftSamples(fr *frame.Frame, shift int) {
	if shift > 0 {
		s := uint(shift)
		for ch := range fr.Channels {
			c := fr.Channels[ch][:fr.BlockSize]
			for i, v := range c {
				c[i] = v << s
			}
		}
		return
	}
	s := uint(-shift)
	for ch := range fr.Channels {
		c := fr.Channels[ch][:fr.BlockSize]
		for i, v := range c {
			c[i] = v >> s
		}
	}
}

package meta

import (
	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// readStreamInfo parses the 34-byte STREAMINFO body. The block header (type and
// length) has already been consumed by the caller. Layout: 16-bit min block size,
// 16-bit max block size, 24-bit min frame size, 24-bit max frame size, 20-bit
// sample rate, 3-bit (channels-1), 5-bit (bps-1), 36-bit total samples, 128-bit MD5.
func readStreamInfo(br *bitio.Reader) (si flac.StreamInfo, minBlock, maxBlock, maxFrame int, err error) {
	mnb, err := br.ReadBits(16)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	mxb, err := br.ReadBits(16)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	if _, err = br.ReadBits(24); err != nil { // min frame size: unused
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	mxf, err := br.ReadBits(24)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	minBlock, maxBlock, maxFrame = int(mnb), int(mxb), int(mxf)
	rate, err := br.ReadBits(20)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	ch, err := br.ReadBits(3)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	bps, err := br.ReadBits(5)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	total, err := br.ReadBits(36)
	if err != nil {
		return flac.StreamInfo{}, 0, 0, 0, err
	}
	// A zero sample rate is invalid, and FLAC bit depth is 4..32 (the 5-bit field
	// stores bps-1, so values 0..2 are below the minimum). Reject malformed
	// STREAMINFO rather than letting bad values reach callers.
	if rate == 0 || bps < 3 {
		return flac.StreamInfo{}, 0, 0, 0, flac.ErrUnsupported
	}
	si.SampleRate = int(rate)
	si.Channels = int(ch) + 1
	si.BitDepth = int(bps) + 1
	si.TotalSamples = total
	for i := range si.MD5 {
		b, err := br.ReadBits(8)
		if err != nil {
			return flac.StreamInfo{}, 0, 0, 0, err
		}
		si.MD5[i] = byte(b)
	}
	return si, minBlock, maxBlock, maxFrame, nil
}

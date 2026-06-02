package meta

import (
	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// readStreamInfo parses the 34-byte STREAMINFO body. The block header (type and
// length) has already been consumed by the caller. Layout: 16-bit min block size,
// 16-bit max block size, 24-bit min frame size, 24-bit max frame size, 20-bit
// sample rate, 3-bit (channels-1), 5-bit (bps-1), 36-bit total samples, 128-bit MD5.
func readStreamInfo(br *bitio.Reader) (flac.StreamInfo, error) {
	var si flac.StreamInfo
	for _, n := range []uint{16, 16, 24, 24} { // min/max block size, min/max frame size: discarded
		if _, err := br.ReadBits(n); err != nil {
			return flac.StreamInfo{}, err
		}
	}
	rate, err := br.ReadBits(20)
	if err != nil {
		return flac.StreamInfo{}, err
	}
	ch, err := br.ReadBits(3)
	if err != nil {
		return flac.StreamInfo{}, err
	}
	bps, err := br.ReadBits(5)
	if err != nil {
		return flac.StreamInfo{}, err
	}
	total, err := br.ReadBits(36)
	if err != nil {
		return flac.StreamInfo{}, err
	}
	si.SampleRate = int(rate)
	si.Channels = int(ch) + 1
	si.BitDepth = int(bps) + 1
	si.TotalSamples = total
	for i := range si.MD5 {
		b, err := br.ReadBits(8)
		if err != nil {
			return flac.StreamInfo{}, err
		}
		si.MD5[i] = byte(b)
	}
	return si, nil
}

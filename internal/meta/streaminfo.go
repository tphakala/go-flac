package meta

import (
	"bytes"
	"fmt"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
)

// DecodeStreamInfo parses a 34-byte STREAMINFO metadata block body into a
// flac.StreamInfo. It is the inverse of EncodeStreamInfo and the entry point a
// container demuxer uses to recover stream properties from a codec box (for
// example an MP4 dfLa box, whose payload is exactly this body). body must be
// StreamInfoBodyLen bytes; the min/max block and frame sizes are validated for
// length but not returned, as a container reader does not need them.
func DecodeStreamInfo(body []byte) (flac.StreamInfo, error) {
	if len(body) != StreamInfoBodyLen {
		return flac.StreamInfo{}, fmt.Errorf("meta: STREAMINFO body is %d bytes, want %d", len(body), StreamInfoBodyLen)
	}
	si, _, _, _, err := readStreamInfo(bitio.NewReader(bytes.NewReader(body)))
	if err != nil {
		return flac.StreamInfo{}, err
	}
	return si, nil
}

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

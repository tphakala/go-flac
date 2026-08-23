package pcm

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/tphakala/go-flac"
)

// DefaultMaxDecodedBytes is the ceiling DecodeInterleaved applies to its output.
// A FLAC stream's decoded length is not proportional to its encoded size: a
// constant subframe encodes a whole 65535-sample block in a handful of bytes, so
// a small crafted file can decode to something on the order of a gigabyte. The
// one-shot helpers hold the whole decode in memory, so they stop at this ceiling
// rather than let a tiny input drive an unbounded allocation. It is 1 GiB, about
// 101 minutes of CD-quality (44.1 kHz, 16-bit) stereo.
//
// A caller decoding a stream it did not produce, or one legitimately longer than
// this, should use NewDecoder and stream the audio instead, which bounds memory
// to a single reusable buffer regardless of length.
const DefaultMaxDecodedBytes = 1 << 30

// ErrDecodeLimit reports that a one-shot decode was stopped because its output
// would exceed the byte ceiling in effect. Test for it with errors.Is. See
// DefaultMaxDecodedBytes for why the limit exists.
var ErrDecodeLimit = errors.New("go-flac/pcm: decoded size limit exceeded")

// DecodeInterleaved reads an entire FLAC stream from r and returns the decoded
// interleaved little-endian PCM together with the stream info. It is the one-shot
// mirror of EncodeInterleaved, and of the sibling one-shot decoders in the
// go-audio family (go-wav's pcm.DecodeInterleaved, go-m4a's flacm4a.DecodeInterleaved).
//
// It stops at DefaultMaxDecodedBytes and returns a wrapped ErrDecodeLimit if the
// output would exceed it. For a different ceiling use DecodeInterleavedLimit; for
// a stream of unknown or unbounded length use NewDecoder, which streams the audio
// in memory proportional to a single buffer.
func DecodeInterleaved(r io.Reader) ([]byte, flac.StreamInfo, error) {
	return DecodeInterleavedLimit(r, DefaultMaxDecodedBytes)
}

// DecodeInterleavedLimit is DecodeInterleaved with a caller-chosen ceiling.
// maxBytes is the largest decoded output it will return; a decode that would
// exceed it stops and returns a wrapped ErrDecodeLimit. A maxBytes of zero or
// less removes the ceiling, which is only safe for a stream the caller produced
// or has otherwise bounded.
func DecodeInterleavedLimit(r io.Reader, maxBytes int) ([]byte, flac.StreamInfo, error) {
	d, err := NewDecoder(r)
	if err != nil {
		return nil, flac.StreamInfo{}, err
	}
	info := d.Info()

	var buf bytes.Buffer
	// Pre-size from the declared length so the common case allocates once instead
	// of growing by doubling. presizeHint bounds the reservation; see there for why
	// the declared count is not trusted directly.
	if n := presizeHint(info, maxBytes); n > 0 {
		buf.Grow(n)
	}

	cw := &cappedWriter{buf: &buf, max: maxBytes}
	if _, err := d.WriteTo(cw); err != nil {
		return nil, info, err
	}
	return buf.Bytes(), info, nil
}

// maxPreSize caps the up-front buffer reservation the one-shot makes from the
// STREAMINFO-declared sample count. That count is read from the header and is not
// verified against the actual stream length, so without a cap a tiny crafted file
// declaring a huge total would drive a reservation of up to the whole ceiling (a
// gibibyte by default) before a single frame is decoded, which is the very
// small-input-large-allocation hazard DefaultMaxDecodedBytes exists to stop. The
// cappedWriter still enforces the real ceiling on the bytes actually produced, so
// this only bounds the initial guess: a genuinely large stream grows past it by
// doubling.
const maxPreSize = 32 << 20 // 32 MiB

// presizeHint returns how many bytes to reserve up front for a decode of the given
// stream, or 0 to skip pre-sizing. It uses the ceil bytes-per-sample the decoder
// actually emits, and is bounded by maxPreSize and, when a positive ceiling is
// set, by maxBytes, so a declared length can never drive the reservation past
// those.
func presizeHint(info flac.StreamInfo, maxBytes int) int {
	if info.TotalSamples == 0 || info.Channels <= 0 || info.BitDepth <= 0 {
		return 0
	}
	bytesPerSample := (info.BitDepth + 7) / 8
	hint := int64(info.TotalSamples) * int64(info.Channels) * int64(bytesPerSample)
	if hint <= 0 { // zero, or a wrapped-negative from an implausible declared total
		return 0
	}
	if hint > maxPreSize {
		hint = maxPreSize
	}
	if maxBytes > 0 && hint > int64(maxBytes) {
		hint = int64(maxBytes)
	}
	return int(hint)
}

// cappedWriter accumulates into buf and refuses a write that would carry the
// total past max. It writes nothing on the failing call, so buf holds only whole
// frames worth of samples up to the point the limit was hit. A max of zero or
// less is unbounded.
type cappedWriter struct {
	buf *bytes.Buffer
	n   int
	max int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.max > 0 && c.n > c.max-len(p) {
		return 0, fmt.Errorf(
			"go-flac/pcm: DecodeInterleaved: %w: output would exceed %d bytes",
			ErrDecodeLimit, c.max)
	}
	n, err := c.buf.Write(p)
	c.n += n
	return n, err
}

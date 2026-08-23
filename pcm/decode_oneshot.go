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
	// Pre-size from the declared length when it is known and fits under the
	// ceiling, so the common case allocates once instead of growing by doubling.
	// The guard keeps a file that declares an implausible length from driving the
	// reservation: the hint is only taken when it is within maxBytes, and the cap
	// below still enforces the ceiling against the bytes actually produced.
	if info.TotalSamples > 0 && info.Channels > 0 && info.BitDepth > 0 {
		if hint := int64(info.TotalSamples) * int64(info.Channels) * int64(info.BitDepth/8); hint > 0 {
			if maxBytes > 0 && hint <= int64(maxBytes) {
				buf.Grow(int(hint))
			}
		}
	}

	cw := &cappedWriter{buf: &buf, max: maxBytes}
	if _, err := d.WriteTo(cw); err != nil {
		return nil, info, err
	}
	return buf.Bytes(), info, nil
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

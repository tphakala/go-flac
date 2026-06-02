package pcm

import (
	"errors"
	"io"

	flac "github.com/tphakala/go-flac"
)

// Decoder decodes a FLAC stream into interleaved little-endian PCM. It
// implements io.Reader and io.WriterTo, and offers sample-accurate Seek when
// the underlying source is an io.Seeker.
type Decoder struct{}

var (
	_ io.Reader   = (*Decoder)(nil)
	_ io.WriterTo = (*Decoder)(nil)
)

// NewDecoder returns a Decoder reading a FLAC stream from r. Decoding is
// implemented in M2; this groundwork stub validates its input and otherwise
// returns flac.ErrNotImplemented.
func NewDecoder(r io.Reader) (*Decoder, error) {
	if r == nil {
		return nil, errors.New("go-flac/pcm: NewDecoder: nil reader")
	}
	return nil, flac.ErrNotImplemented
}

// Read fills p with decoded interleaved little-endian PCM.
func (*Decoder) Read(_ []byte) (int, error) { return 0, flac.ErrNotImplemented }

// WriteTo drains all decoded PCM into w without an intermediate caller buffer.
func (*Decoder) WriteTo(_ io.Writer) (int64, error) { return 0, flac.ErrNotImplemented }

// SeekToSample moves the read position to the given inter-channel sample
// index. It returns ErrSeekUnsupported when the source is not seekable.
func (*Decoder) SeekToSample(_ int64) (int64, error) { return 0, flac.ErrNotImplemented }

// Info returns the stream's STREAMINFO-derived properties.
func (*Decoder) Info() flac.StreamInfo { return flac.StreamInfo{} }

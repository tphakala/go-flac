package pcm

import (
	"errors"
	"fmt"
	"io"

	flac "github.com/tphakala/go-flac"
)

// Encoder encodes interleaved little-endian PCM written to it into a FLAC
// stream. It implements io.WriteCloser; Close flushes the final frame and
// finalizes the STREAMINFO MD5.
type Encoder struct{}

var _ io.WriteCloser = (*Encoder)(nil)

// NewEncoder returns an Encoder that writes a FLAC stream to w using cfg.
// Encoding is implemented in M3; this groundwork stub validates its inputs and
// otherwise returns flac.ErrNotImplemented.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("go-flac/pcm: NewEncoder: nil writer")
	}
	if cfg.SampleRate <= 0 || cfg.BitDepth <= 0 || cfg.Channels <= 0 {
		return nil, fmt.Errorf("go-flac/pcm: NewEncoder: invalid config %+v", cfg)
	}
	return nil, flac.ErrNotImplemented
}

// Write consumes interleaved little-endian PCM samples.
func (*Encoder) Write(_ []byte) (int, error) { return 0, flac.ErrNotImplemented }

// Close flushes buffered samples, writes the final frame, and finalizes the
// stream MD5 and total-sample count.
func (*Encoder) Close() error { return flac.ErrNotImplemented }

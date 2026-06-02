// Package pcm is the high-level, clean-room PCM streaming API for go-flac.
//
// It bridges raw interleaved little-endian PCM and the FLAC codec core,
// exposing an io.WriteCloser encoder and an io.Reader / io.WriterTo decoder
// with optional sample-accurate Seek. The decoder lands in M2 and the encoder
// in M3; in the groundwork skeleton the constructors validate their inputs and
// otherwise return flac.ErrNotImplemented.
package pcm

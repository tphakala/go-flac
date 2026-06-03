// Package pcm is the high-level, clean-room PCM streaming API for go-flac.
//
// It bridges raw interleaved little-endian PCM and the FLAC codec core,
// exposing an io.WriteCloser encoder and an io.Reader / io.WriterTo decoder.
// The decoder (NewDecoder) supports sample-accurate SeekToSample when the
// underlying source implements io.Seeker.
package pcm

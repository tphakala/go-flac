// Package pcm is the high-level, clean-room PCM streaming API for go-flac.
//
// It bridges raw interleaved little-endian PCM and the FLAC codec core,
// exposing an io.WriteCloser encoder and an io.Reader / io.WriterTo decoder.
// The decoder (NewDecoder) landed in M2 and the encoder (NewEncoder) in M3;
// both are implemented. Sample-accurate Seek is deferred to a later milestone.
package pcm

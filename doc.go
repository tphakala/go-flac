// Package flac provides a native Go FLAC encoder and decoder. It uses no CGO and
// no external binaries; the encoder hot paths are SIMD-accelerated behind a
// pure-Go fallback (bit-identical output), so it builds and runs on every Go
// target.
//
// The high-level PCM streaming API lives in the pcm subpackage
// (github.com/tphakala/go-flac/pcm). Use pcm.NewDecoder to decode a FLAC
// stream to interleaved little-endian PCM, and pcm.NewEncoder to encode
// interleaved little-endian PCM to a FLAC stream. This root package exposes
// only shared types used across the public API. Codec internals live under
// internal/.
package flac

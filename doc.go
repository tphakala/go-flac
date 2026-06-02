// Package flac provides a native, pure-Go FLAC encoder and decoder.
//
// The high-level PCM streaming API lives in the pcm subpackage
// (github.com/tphakala/go-flac/pcm). Use pcm.NewDecoder to decode a FLAC
// stream to interleaved little-endian PCM, and pcm.NewEncoder to encode
// interleaved little-endian PCM to a FLAC stream. This root package exposes
// only shared types used across the public API. Codec internals live under
// internal/.
package flac

package flac

import "errors"

// Version is the current library version. go-flac follows semantic
// versioning; "0.0.0-dev" marks the pre-release groundwork skeleton.
const Version = "0.0.0-dev"

// ErrNotImplemented is returned by API surfaces that exist as scaffolding but
// whose behavior is not yet built. Each milestone removes the uses it lands.
var ErrNotImplemented = errors.New("go-flac: not implemented")

// StreamInfo describes a FLAC stream's global properties, mirroring the
// STREAMINFO metadata block.
type StreamInfo struct {
	SampleRate   int      // samples per second, e.g. 44100
	Channels     int      // number of channels, 1..8
	BitDepth     int      // bits per sample, e.g. 16 or 24
	TotalSamples uint64   // inter-channel sample count; 0 if unknown
	MD5          [16]byte // MD5 of the unencoded audio; all-zero if absent
}

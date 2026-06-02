package flac

import "errors"

// Version is the current library version. go-flac follows semantic
// versioning; "0.0.0-dev" marks the pre-release groundwork skeleton.
const Version = "0.0.0-dev"

// Sentinel errors returned by the decoder and the pcm streaming layer.
// Callers can test for them with errors.Is.
var (
	// ErrNotImplemented marks API surfaces whose behavior is not yet built.
	ErrNotImplemented = errors.New("go-flac: not implemented")
	// ErrSeekUnsupported is returned by SeekToSample when the source is not seekable.
	ErrSeekUnsupported = errors.New("go-flac: seek unsupported (source is not an io.Seeker)")
	// ErrMissingStreamInfo means the stream had no STREAMINFO metadata block, or it did
	// not appear first.
	ErrMissingStreamInfo = errors.New("go-flac: missing or misplaced STREAMINFO block")
	// ErrCRCMismatch means a frame header CRC-8 or frame CRC-16 did not match.
	ErrCRCMismatch = errors.New("go-flac: CRC mismatch")
	// ErrMD5Mismatch means the decoded-audio MD5 did not match the STREAMINFO MD5.
	ErrMD5Mismatch = errors.New("go-flac: MD5 mismatch")
	// ErrUnsupported marks a reserved or illegal coded value, or an unsupported layout.
	ErrUnsupported = errors.New("go-flac: unsupported or reserved bitstream value")
)

// StreamInfo describes a FLAC stream's global properties, mirroring the
// STREAMINFO metadata block.
type StreamInfo struct {
	SampleRate   int      // samples per second, e.g. 44100
	Channels     int      // number of channels, 1..8
	BitDepth     int      // bits per sample, e.g. 16 or 24
	TotalSamples uint64   // inter-channel sample count; 0 if unknown
	MD5          [16]byte // MD5 of the unencoded audio; all-zero if absent
}

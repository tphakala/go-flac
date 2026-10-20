package flac

import "errors"

// Version is the current library version. go-flac follows semantic versioning.
const Version = "1.4.0"

// Sentinel errors returned by the decoder and the pcm streaming layer.
// Callers can test for them with errors.Is.
var (
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
	// ErrEncoderClosed is returned by Encoder.Write after Close has been called.
	ErrEncoderClosed = errors.New("go-flac: encoder is closed")
	// ErrTruncatedStream means the stream ended before delivering the sample count
	// STREAMINFO declares (an inter-frame cut on a zero-MD5 stream), or a frame body
	// was cut short. Mid-frame truncation wraps io.ErrUnexpectedEOF.
	ErrTruncatedStream = errors.New("go-flac: truncated stream")
	// ErrSampleOutOfRange means a frame passed its CRC checks but decoded to a
	// sample outside its bit depth: a subframe sample outside the width that
	// subframe is coded at, or a stereo-decoded sample outside the frame's bit
	// depth. It also covers a Rice-coded residual outside the signed 32-bit range
	// (RFC 9639 section 9.2.7.3), which cannot be decoded into 32-bit residuals
	// without wrapping. A conforming encoder never produces such a frame. libFLAC
	// 1.5.0 also rejects a frame whose decoded output leaves the frame bit depth
	// (stream_decoder.c, lines 2457 to 2470), but it does not check each subframe
	// at its own width as this decoder does, so it accepts some crafted frames
	// that wrap back into range. The decoder reports the error instead of
	// returning samples that depend on the arithmetic route taken.
	ErrSampleOutOfRange = errors.New("go-flac: decoded sample outside the frame bit depth")
	// ErrInvalidSeek means SeekToSample was called with a negative sample index.
	ErrInvalidSeek = errors.New("go-flac: invalid seek (negative sample index)")
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

// Tag is one VORBIS_COMMENT field: a case-insensitive field name and its UTF-8
// value, carried in the stream as "NAME=value". Tags are an ordered list rather
// than a map because the FLAC/Vorbis comment format preserves order and permits
// repeated field names (for example several ARTIST fields). Name must be ASCII in
// the range 0x20..0x7D excluding '=' (0x3D). When encoding, Value must be valid
// UTF-8 text; the encoder rejects a value that is not. When decoding, Value carries
// the stored comment bytes verbatim, so a value read back from a stream that another
// tool wrote may not be valid UTF-8.
type Tag struct {
	Name  string
	Value string
}

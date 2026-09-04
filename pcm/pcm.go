package pcm

import flac "github.com/tphakala/go-flac"

// Config controls encoder output.
type Config struct {
	SampleRate int // samples per second, e.g. 44100
	BitDepth   int // bits per sample, e.g. 16 or 24
	Channels   int // number of channels, 1..8

	// CompressionLevel selects encoder effort from 0 (fastest) to 8 (smallest),
	// matching libFLAC's level meaning. The zero value is level 0. Levels 0-2 use
	// fixed predictors and differ only in stereo decorrelation: 0 codes channels
	// independently, 1 uses adaptive mid-side, 2 searches all stereo modes. Levels
	// 3-8 add LPC (linear predictive coding) with increasing maximum order and
	// deeper residual-partition search, so they compress progressively better.
	// Out-of-range values are clamped to 0-8.
	CompressionLevel int

	// SeekTableInterval, when > 0, makes the encoder emit a SEEKTABLE with one seek
	// point roughly every SeekTableInterval inter-channel samples (plus a point at
	// sample 0). It requires the sink to be an io.WriteSeeker. Zero (the default)
	// emits no SEEKTABLE.
	SeekTableInterval int
	// SeekTableMaxPoints caps the reserved placeholder size; zero selects a default.
	// A stream longer than SeekTableMaxPoints*SeekTableInterval samples leaves its
	// tail without seek points (still seekable via binary search).
	SeekTableMaxPoints int

	// TotalSamples, when > 0, declares the total inter-channel sample count so the
	// encoder writes STREAMINFO.total_samples into the header up front. This lets a
	// non-seekable sink (bytes.Buffer, io.Pipe) emit a finalized total_samples with
	// no seek-back. It must equal the number of samples actually written before
	// Close, which Close verifies; a mismatch is an error. The maximum is 2^36-1
	// (the FLAC 36-bit field); a larger value is rejected at construction. Leave it
	// 0 (the default) when the length is unknown up front. MD5 is unaffected (it
	// stays at the all-zero sentinel for a non-seekable streaming sink); use
	// EncodeInterleaved for an up-front MD5.
	TotalSamples uint64

	// SkipMD5, when true, disables the STREAMINFO MD5 signature: the encoder writes
	// the all-zero "unknown" MD5 (spec-legal) and skips hashing the input PCM, removing
	// the MD5 pass from the encode hot path for callers that do not need the integrity
	// signature or compute it out of band. The encoded audio and every other STREAMINFO
	// field are unchanged; only the 16-byte MD5 goes from the input digest to all zeros.
	// It applies to the streaming Encoder, the one-shot EncodeInterleaved, and
	// FrameEncoder. The default (false) hashes the input and writes the digest.
	SkipMD5 bool

	// Tags, when non-empty, makes the streaming Encoder and the one-shot
	// EncodeInterleaved write a VORBIS_COMMENT metadata block carrying these fields in
	// order, so the FLAC file embeds its own textual tags (title, artist, or any
	// application-specific key). Each Tag.Name must be ASCII 0x20..0x7D excluding '=' and
	// each Tag.Value must be valid UTF-8, both validated when that block is built
	// (NewEncoder, Reset, or EncodeInterleaved); duplicate names are allowed and order
	// is preserved. FrameEncoder writes no metadata region and ignores Tags entirely.
	// The decoder surfaces them via Decoder.Tags.
	Tags []flac.Tag

	// Vendor sets the VORBIS_COMMENT vendor string, which must be valid UTF-8. The empty
	// default becomes "go-flac <version>" whenever a VORBIS_COMMENT block is written. A
	// VORBIS_COMMENT block is written only when Tags is non-empty or Vendor is set, so the
	// default (no tags, no vendor) stays byte-identical to a stream with no tags at all.
	Vendor string
}

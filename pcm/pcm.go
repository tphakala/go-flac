package pcm

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
}

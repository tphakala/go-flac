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
	// 3-8 add LPC (linear predictive coding) with increasing maximum order, deeper
	// residual-partition search, and, from level 4, exhaustive fixed-order
	// selection, so they compress progressively better. Out-of-range values are
	// clamped to 0-8.
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
}

package pcm

// Config controls encoder output.
type Config struct {
	SampleRate int // samples per second, e.g. 44100
	BitDepth   int // bits per sample, e.g. 16 or 24
	Channels   int // number of channels, 1..8

	// CompressionLevel selects encoder effort from 0 (fastest) to 8 (smallest),
	// matching libFLAC's level meaning. The zero value is level 0. In this release
	// the encoder is fixed-predictor only: levels 0-2 are fully realized (0 codes
	// channels independently, 1 uses adaptive mid-side, 2 searches all stereo
	// modes); levels 3-8 set deeper residual-partition search but do not yet enable
	// LPC, so they currently compress about like level 2 and improve automatically
	// when LPC lands. Out-of-range values are clamped to 0-8.
	CompressionLevel int
}

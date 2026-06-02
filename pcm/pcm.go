package pcm

// Config controls encoder output.
type Config struct {
	SampleRate int // samples per second, e.g. 44100
	BitDepth   int // bits per sample, e.g. 16 or 24
	Channels   int // number of channels, 1..8

	// CompressionLevel selects encoder effort from 0 (fastest) to 8
	// (smallest), matching libFLAC's level meaning.
	CompressionLevel int
}

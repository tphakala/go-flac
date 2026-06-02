package frame

// StereoMode selects how a 2-channel frame chooses its channel assignment.
type StereoMode int

const (
	// StereoIndependent always codes left/right independently.
	StereoIndependent StereoMode = iota
	// StereoAdaptive picks the cheaper of independent and mid/side by estimate.
	StereoAdaptive
	// StereoFull estimates all four assignments and picks the cheapest.
	StereoFull
)

// Params are the per-frame encoder knobs derived from the compression level.
type Params struct {
	Stereo            StereoMode
	MaxPartitionOrder int
	ExhaustiveFixed   bool
}

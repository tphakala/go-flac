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

// Apodization selects the analysis window used for LPC. M3b ships a single
// window (Tukey(0.5)); the enum exists so future windows can be added without
// changing call sites. ApodTukey05 is the zero value so a zero Params defaults
// to it.
type Apodization int

const (
	ApodTukey05 Apodization = iota // Tukey(0.5)
)

// Params are the per-frame encoder knobs derived from the compression level.
type Params struct {
	Stereo            StereoMode
	MaxPartitionOrder int
	ExhaustiveFixed   bool
	MaxLPCOrder       int         // 0 = fixed only (levels 0-2); max LPC order to consider
	LPCPrecision      int         // quantized coefficient precision in bits; 0 when MaxLPCOrder == 0
	Apodization       Apodization // analysis window; zero value is Tukey(0.5)
}

package frame

import (
	"math"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// TestStereoCandidateRoundTrip guards the per-candidate carried scratch added in
// M5a Task 4. Every stereo frame plans all four candidates (L, R, M, S) into the
// four workspace slots regardless of which decorrelation mode wins, so any slot
// that shares backing storage with another would corrupt the carried residuals or
// plan and break the round-trip. The richest path (StereoFull + exhaustive fixed +
// LPC) is exercised at both bps 16 (encodeStereo, int32 slots) and bps 25
// (encodeStereo64, int64 slots).
func TestStereoCandidateRoundTrip(t *testing.T) {
	const bs = 4096
	p := Params{Stereo: StereoFull, MaxPartitionOrder: 6, ExhaustiveFixed: true, MaxLPCOrder: 12, LPCPrecision: 15}

	// gen builds an l/r pair of the given amplitude for the four decorrelation
	// regimes. amp bounds each sample so the wide (bps 25) cases stay within
	// +-2^24 (side = l-r can reach 2*amp, so amp <= 2^23 keeps side under 2^24).
	gen := func(kind string, amp float64) (l, r []int32) {
		l = make([]int32, bs)
		r = make([]int32, bs)
		for i := range bs {
			t0 := float64(i)
			switch kind {
			case "independent":
				// Two unrelated tones at different frequencies/phases.
				l[i] = int32(amp * math.Sin(t0*0.013))
				r[i] = int32(amp * math.Sin(t0*0.041+1.7))
			case "midside":
				// Strongly correlated: r tracks l with a small decorrelated jitter,
				// so mid carries the signal and side is small (favors mid-side).
				base := amp * math.Sin(t0*0.017)
				l[i] = int32(base + amp*0.02*math.Sin(t0*0.31))
				r[i] = int32(base - amp*0.02*math.Sin(t0*0.31))
			case "identical":
				// l == r exactly, so side == 0 (favors a side mode).
				v := int32(amp * math.Sin(t0*0.023))
				l[i] = v
				r[i] = v
			case "anti":
				// r = -l, so mid is near zero and side carries the signal.
				v := int32(amp * math.Sin(t0*0.029))
				l[i] = v
				r[i] = -v
			}
		}
		return l, r
	}

	cases := []string{"independent", "midside", "identical", "anti"}

	// bps 16: amplitude near full scale for 16-bit (side = l-r stays within int17).
	for _, kind := range cases {
		l, r := gen(kind, 30000)
		roundTripFrame(t, flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16},
			p, [][]int32{l, r}, 0)
	}

	// bps 25: amplitude bounded so |l|,|r| <= 2^23 and |side| = |l-r| <= 2^24.
	const wideAmp = 1 << 23
	for _, kind := range cases {
		l, r := gen(kind, wideAmp)
		roundTripFrame(t, flac.StreamInfo{SampleRate: 96000, Channels: 2, BitDepth: 25},
			p, [][]int32{l, r}, 1)
	}
}

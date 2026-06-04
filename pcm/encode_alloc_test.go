package pcm

import (
	"io"
	"testing"
)

// TestEncodeSteadyStateAllocs is the M5a public-API allocation guard. It encodes
// many full blocks through one pcm.Encoder writing to io.Discard and asserts that
// the steady-state per-block allocation count stays at or below 2. The M5a work
// moved every per-frame scratch buffer into a reused frame.Workspace, so a healthy
// steady state allocates essentially nothing per block; the bound of 2 only allows
// for unavoidable interface churn (e.g. io.Writer boxing). A regression that
// reintroduces per-frame heap traffic will push this well above 2 and redden here.
//
// testing.AllocsPerRun runs the function once to warm up before it starts counting,
// so the first-frame buffer and apodization-window growth are not included in the
// measured figure.
func TestEncodeSteadyStateAllocs(t *testing.T) {
	cases := []struct {
		name  string
		level int
	}{
		{"level5_16bit_stereo", 5},
		{"level8_16bit_stereo", 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: tc.level}
			enc, err := NewEncoder(io.Discard, cfg)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			// One full block of interleaved little-endian PCM:
			// encoderBlockSize samples * channels * bytes-per-sample.
			bytesPS := (cfg.BitDepth + 7) / 8
			block := make([]byte, encoderBlockSize*cfg.Channels*bytesPS)
			// Fill with a mildly compressible pattern (a ramp plus a low-bit wobble)
			// so the encoder takes its normal predictor/Rice path with no wasted bits.
			for i := range block {
				block[i] = byte(i*7 + (i>>3)*3)
			}
			allocs := testing.AllocsPerRun(50, func() {
				if _, err := enc.Write(block); err != nil {
					t.Fatalf("Write: %v", err)
				}
			})
			t.Logf("%s: steady-state encode allocates %.2f allocs/op", tc.name, allocs)
			if allocs > 2 {
				t.Fatalf("%s: steady-state encode allocates %.0f/op (want <= 2)", tc.name, allocs)
			}
		})
	}
}

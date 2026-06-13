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

// TestEncodeResetReuseAllocs is the Reset-and-reuse allocation guard for the
// encoder-pooling case (pcm.Encoder.Reset). After the encoder has been built
// once, rebinding it to a fresh sink with the *same shape* must not re-allocate
// the large per-stream buffers (the multi-MB frame.Workspace and the per-channel
// block buffers). A same-shape Reset only re-emits the small fixed metadata
// header, so its allocation count stays tiny and far below a fresh NewEncoder
// (which allocates the whole workspace). A regression that drops the buffer-reuse
// fast path would reallocate the workspace and redden here.
//
// testing.AllocsPerRun warms up once before counting, so the first construction
// (which legitimately allocates everything) is excluded from the measured figure.
func TestEncodeResetReuseAllocs(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 8}
	enc, err := NewEncoder(io.Discard, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	// Encode one full stream so every reusable buffer exists and is in its
	// post-Close state before we measure Reset.
	bytesPS := (cfg.BitDepth + 7) / 8
	block := make([]byte, encoderBlockSize*cfg.Channels*bytesPS)
	for i := range block {
		block[i] = byte(i*7 + (i>>3)*3)
	}
	if _, err := enc.Write(block); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A same-shape Reset re-emits only the fixed metadata header (~5 allocs/op
	// here); a fresh NewEncoder of this config costs ~40 allocs/op because it builds
	// the whole workspace. The bound sits well below that 40 so any regression that
	// reallocates the workspace per stream reddens this guard, while leaving margin
	// for minor header-serialization churn across Go versions.
	const maxAllocs = 8
	allocs := testing.AllocsPerRun(50, func() {
		if err := enc.Reset(io.Discard, cfg); err != nil {
			t.Fatalf("Reset: %v", err)
		}
	})
	t.Logf("same-shape Reset allocates %.2f allocs/op", allocs)
	if allocs > maxAllocs {
		t.Fatalf("same-shape Reset allocates %.0f/op (want <= %d); the buffer-reuse fast path likely regressed", allocs, maxAllocs)
	}
}

// TestClosePreservesLeftoverForReuse guards the pooled-encoder allocation
// contract: Close must retain the leftover buffer's backing array (truncating it
// to zero length, not nilling it) so a subsequent Reset + Write of a stream with a
// partial trailing block reuses that array instead of reallocating it. A stream
// whose length is not an exact multiple of the block size (the common case) routes
// its tail through e.leftover, so dropping the backing array in Close would make a
// pooled encoder reallocate it for every clip, defeating the zero-allocation goal
// for back-to-back short clips.
func TestClosePreservesLeftoverForReuse(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	enc, err := NewEncoder(io.Discard, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	// One full block plus a 50-sample partial block: the tail lands in e.leftover
	// and is flushed as the final short block by Close.
	if _, err := enc.Write(genPCM(cfg, encoderBlockSize+50)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cap(enc.leftover) == 0 {
		t.Fatal("Close discarded the leftover backing array; a pooled encoder would reallocate it for every clip with a partial final block")
	}
}

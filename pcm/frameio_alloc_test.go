package pcm

import "testing"

// bitioReadBlock is the read block FrameDecoder's reader uses: the bitio default
// (readBlock in internal/bitio/reader.go, 8 KiB), which a zero-value Reader gets on
// its first Reset. Rows marked largeFrame must exceed it so one frame takes several
// refills, each compacting the reused buffer.
const bitioReadBlock = 8 << 10

// TestFrameDecoderSteadyStateAllocs guards the demux decode path: once the first
// call has sized the decoder's reused buffers, decoding more frames through one
// FrameDecoder allocates nothing. A bit reader built per frame showed up here as 2
// allocs per frame (its read block and the bytes.Reader). AllocsPerRun warms up with
// one run, and every run decodes all frames including the short final one, so all
// buffers are at their high-water size before counting starts.
func TestFrameDecoderSteadyStateAllocs(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		gen        func(Config, int) []byte
		largeFrame bool
	}{
		// genPCM's ramp codes to about 6 KB per stereo frame, so a whole frame
		// arrives in one read.
		{"stereo16_frame_under_block", Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}, genPCM, false},
		// Noisy audio codes close to verbatim (about 23 KB per 24-bit stereo frame).
		{"stereo24_frame_over_block", Config{SampleRate: 48000, Channels: 2, BitDepth: 24, CompressionLevel: 5}, genRealisticPCM, true},
		// 25 to 32 bits per sample decode through the int64 subframe path.
		{"stereo32_wide_path", Config{SampleRate: 48000, Channels: 2, BitDepth: 32, CompressionLevel: 5}, genRealisticPCM, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames, _, enc := collectFrames(t, tc.cfg, tc.gen(tc.cfg, encoderBlockSize*3+100))
			// Keep the fixture honest: the full frames must sit on the intended side
			// of the read block, or the row stops covering what its name says.
			for i, fr := range frames[:len(frames)-1] {
				if (len(fr) > bitioReadBlock) != tc.largeFrame {
					t.Fatalf("frame %d is %d bytes; row expects largeFrame=%v against a %d byte block", i, len(fr), tc.largeFrame, bitioReadBlock)
				}
			}
			dec, err := NewFrameDecoder(enc.StreamInfoBytes())
			if err != nil {
				t.Fatalf("NewFrameDecoder: %v", err)
			}
			allocs := testing.AllocsPerRun(20, func() {
				for _, fr := range frames {
					if _, _, err := dec.DecodeInterleaved(fr); err != nil {
						t.Fatalf("DecodeInterleaved: %v", err)
					}
				}
			})
			if allocs != 0 {
				t.Fatalf("decoding %d frames allocates %.1f objects per run, want 0", len(frames), allocs)
			}
		})
	}
}

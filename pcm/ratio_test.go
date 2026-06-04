package pcm

import (
	"bytes"
	"testing"
)

// TestCompressionRatioFloor guards a minimum compression quality by capping the
// output/input byte ratio on the realistic signal, so estimate-driven selection
// cannot silently regress compression (lower ratio is better, so the cap is a
// floor on quality). maxRatio is the measured post-refactor ratio plus a small
// margin; raise it only with a deliberate, reviewed change.
func TestCompressionRatioFloor(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	in := genRealisticPCM(cfg, 48000*4)
	var out bytes.Buffer
	enc, err := NewEncoder(&out, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	ratio := float64(out.Len()) / float64(len(in))
	t.Logf("compression ratio = %.4f (out=%d in=%d)", ratio, out.Len(), len(in))
	// maxRatio is the enforced ceiling on the output/input ratio: 0 disables the
	// gate (unarmed, log only); a positive value enforces it. Armed at the measured
	// 0.9075 plus a 0.002 margin.
	const maxRatio = 0.9095
	if maxRatio > 0 && ratio > maxRatio {
		t.Fatalf("compression ratio regressed: got %.4f want <= %.4f", ratio, maxRatio)
	}
}

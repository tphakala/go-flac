package pcm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// m5GoldenCase is one cell of the byte-identical matrix.
type m5GoldenCase struct {
	Level    int
	Channels int
	BitDepth int
	Rate     int
	Samples  int // inter-channel sample count
}

// goldenMatrix covers the encoder decision surface: levels (fixed-only,
// adaptive-stereo, LPC, max-LPC), channel counts (mono, stereo decorrelation,
// >2 independent), bit depths (byte-aligned + non-aligned + wide int64 path),
// and a short final block (Samples not a multiple of 4096).
func goldenMatrix() []m5GoldenCase {
	cs := make([]m5GoldenCase, 0, 41)
	for _, level := range []int{0, 2, 5, 8} {
		for _, ch := range []int{1, 2, 3} {
			for _, bps := range []int{8, 16, 24} {
				cs = append(cs, m5GoldenCase{level, ch, bps, 44100, 8192})
			}
		}
	}
	// Wide int64 path (25-32 bps), stereo, a couple of levels; plus short final
	// blocks (8192+1234 and 4096+777 force a partial last frame).
	cs = append(cs,
		m5GoldenCase{5, 2, 25, 96000, 8192},
		m5GoldenCase{5, 2, 32, 192000, 8192},
		m5GoldenCase{8, 2, 32, 192000, 8192},
		m5GoldenCase{5, 2, 16, 44100, 8192 + 1234},
		m5GoldenCase{8, 1, 24, 48000, 4096 + 777},
	)
	return cs
}

func (c m5GoldenCase) key() string {
	return fmt.Sprintf("L%d_ch%d_bps%d_r%d_n%d", c.Level, c.Channels, c.BitDepth, c.Rate, c.Samples)
}

// synthPCM builds deterministic interleaved int32 PCM for a case: a seeded mix
// of a sine-like ramp and PRNG noise, clamped to the signed range for bps.
func (c m5GoldenCase) synthPCM() []int32 {
	r := rand.New(rand.NewSource(int64(c.Level*1_000_003 + c.Channels*9176 + c.BitDepth*31 + c.Samples)))
	maxv := int32(1)<<(c.BitDepth-1) - 1
	minv := -maxv - 1
	out := make([]int32, c.Samples*c.Channels)
	for i := range c.Samples {
		for ch := range c.Channels {
			// Correlated base across channels (so stereo decorrelation engages)
			// plus per-channel noise and a wasted-bits-friendly low bit pattern.
			v := int32((i*73+ch*9301)%(int(maxv/4)+1)) + int32(r.Intn(257)) - 128
			if v > maxv {
				v = maxv
			}
			if v < minv {
				v = minv
			}
			out[i*c.Channels+ch] = v
		}
	}
	return out
}

// packLE packs interleaved int32 PCM into little-endian two's-complement bytes.
// It is the exact inverse of the encoder's deinterleave step: bytesPS bytes per
// sample, low bytes first.
func packLE(pcm []int32, bps int) []byte {
	bytesPS := (bps + 7) / 8
	out := make([]byte, len(pcm)*bytesPS)
	for i, s := range pcm {
		u := uint32(s)
		base := i * bytesPS
		for b := range bytesPS {
			out[base+b] = byte(u >> (8 * b))
		}
	}
	return out
}

// encodeCase encodes one case to a FLAC byte stream via the public Encoder.
func encodeCase(t *testing.T, c m5GoldenCase) []byte {
	t.Helper()
	pcm := c.synthPCM()
	var buf seekBuffer // in-memory io.WriteSeeker (see encode_testhelpers_test.go)
	enc, err := NewEncoder(&buf, Config{
		SampleRate:       c.Rate,
		Channels:         c.Channels,
		BitDepth:         c.BitDepth,
		CompressionLevel: c.Level,
	})
	if err != nil {
		t.Fatalf("%s: NewEncoder: %v", c.key(), err)
	}
	// Feed as little-endian packed PCM matching the encoder's Write contract.
	if _, err := enc.Write(packLE(pcm, c.BitDepth)); err != nil {
		t.Fatalf("%s: Write: %v", c.key(), err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("%s: Close: %v", c.key(), err)
	}
	return buf.Bytes()
}

func TestEncoderByteIdenticalGolden(t *testing.T) {
	goldenPath := filepath.Join("testdata", "m5_golden.json")
	record := os.Getenv("M5_RECORD") == "1"

	got := map[string]string{}
	for _, c := range goldenMatrix() {
		sum := sha256.Sum256(encodeCase(t, c))
		got[c.key()] = hex.EncodeToString(sum[:])
	}

	if record {
		keys := slices.Sorted(maps.Keys(got))
		ordered := map[string]string{}
		for _, k := range keys {
			ordered[k] = got[k]
		}
		b, _ := json.MarshalIndent(ordered, "", "  ")
		if err := os.WriteFile(goldenPath, append(b, '\n'), 0o644); err != nil {
			t.Fatalf("record golden: %v", err)
		}
		t.Logf("recorded %d golden hashes to %s", len(got), goldenPath)
		return
	}

	want := map[string]string{}
	b, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with M5_RECORD=1 once to create it): %v", err)
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("case count drift: got %d, golden %d", len(got), len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: output changed\n golden %s\n got    %s", k, w, got[k])
		}
	}
}

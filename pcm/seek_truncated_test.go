package pcm

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// encodeNoise encodes seeded noise at level 2 (4096-sample blocks). Noise barely
// compresses, so the stream size is predictable from the shape. A seekable sink
// records TotalSamples; a plain bytes.Buffer leaves it 0 (unknown length).
func encodeNoise(t *testing.T, channels, bps, samples int, knownTotal bool) []byte {
	t.Helper()
	cfg := Config{SampleRate: 48000, Channels: channels, BitDepth: bps, CompressionLevel: 2}
	r := rand.New(rand.NewSource(int64(samples)))
	pcm := make([]byte, samples*channels*((bps+7)/8))
	for i := range pcm {
		pcm[i] = byte(r.Intn(256))
	}
	var sb seekBuffer
	var buf bytes.Buffer
	var w io.Writer = &buf
	if knownTotal {
		w = &sb
	}
	enc, err := NewEncoder(w, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if knownTotal {
		return sb.Bytes()
	}
	return buf.Bytes()
}

// TestSeekIntoCutLastFrame pins that a seek whose target lies in a final frame cut
// short by the end of the source reports ErrTruncatedStream (wrapping
// io.ErrUnexpectedEOF), sticky like the other seek failures, instead of a position
// before the target with a nil error. Targets in the intact frames before the cut
// still land exactly, and reading on from them reports the cut when it is reached.
func TestSeekIntoCutLastFrame(t *testing.T) {
	const block = 4096 // level 2 block size
	for _, c := range []struct {
		name                   string
		channels, bps, samples int
		knownTotal, overLinear bool
	}{
		{"large known total", 8, 24, 6 * block, true, true},
		{"large unknown total", 8, 24, 6 * block, false, true},
		{"small known total", 2, 16, 12000, true, false},
		{"small unknown total", 2, 16, 12000, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			full := encodeNoise(t, c.channels, c.bps, c.samples, c.knownTotal)
			frameBytes := c.channels * ((c.bps + 7) / 8)
			want, err := io.ReadAll(mustDecoder(t, bytes.NewReader(full)))
			if err != nil {
				t.Fatal(err)
			}
			lastFirst := (c.samples - 1) / block * block
			lastStart := len(truncateAtFrameBoundary(t, full, lastFirst/block))
			cut := full[:lastStart+(len(full)-lastStart)/2] // mid-body of the last frame
			const linearWindow = 1 << 16                    // searchFrame's linear-scan threshold
			if c.overLinear != (len(cut) > linearWindow) {
				t.Fatalf("cut stream is %d bytes; the case needs it on the other side of the 64 KiB linear window", len(cut))
			}

			cutTargets := []int64{int64(lastFirst), int64(c.samples - 1)}
			if !c.knownTotal {
				cutTargets = append(cutTargets, int64(c.samples+5000))
			}
			for _, tg := range cutTargets {
				dec, err := NewDecoder(bytes.NewReader(cut), SkipMD5Verification())
				if err != nil {
					t.Fatal(err)
				}
				landed, err := dec.SeekToSample(tg)
				if !errors.Is(err, flac.ErrTruncatedStream) || !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("SeekToSample(%d) = (%d, %v), want an error wrapping ErrTruncatedStream and io.ErrUnexpectedEOF", tg, landed, err)
				}
				if n, rerr := dec.Read(make([]byte, 4)); n != 0 || !errors.Is(rerr, flac.ErrTruncatedStream) {
					t.Fatalf("Read after the failed seek = (%d, %v), want (0, the sticky ErrTruncatedStream)", n, rerr)
				}
			}

			// The last sample of the last intact frame. For the large shape the binary
			// search probes the cut frame on the way here, so a search that treated a
			// cut probe as fatal fails this target.
			tg := int64(lastFirst - 1)
			dec, err := NewDecoder(bytes.NewReader(cut), SkipMD5Verification())
			if err != nil {
				t.Fatal(err)
			}
			landed, err := dec.SeekToSample(tg)
			if err != nil || landed != tg {
				t.Fatalf("SeekToSample(%d) = (%d, %v), want (%d, nil)", tg, landed, err, tg)
			}
			got, rerr := io.ReadAll(dec)
			if !errors.Is(rerr, flac.ErrTruncatedStream) || !errors.Is(rerr, io.ErrUnexpectedEOF) {
				t.Fatalf("read on from %d: err = %v, want ErrTruncatedStream wrapping io.ErrUnexpectedEOF", tg, rerr)
			}
			if wantRest := want[int(tg)*frameBytes : lastFirst*frameBytes]; !bytes.Equal(got, wantRest) {
				t.Fatalf("read on from %d: got %d bytes, want the %d intact bytes before the cut frame", tg, len(got), len(wantRest))
			}
		})
	}
}

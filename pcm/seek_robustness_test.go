package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/meta"
)

// seekEndUnsupported is an io.ReadSeeker that supports SeekStart and SeekCurrent but
// rejects SeekEnd, like some streaming sources. NewDecoder must degrade to forward-only
// instead of failing construction.
type seekEndUnsupported struct{ r *bytes.Reader }

func (s seekEndUnsupported) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s seekEndUnsupported) Seek(off int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 0, errors.New("SeekEnd not supported")
	}
	return s.r.Seek(off, whence)
}

func TestNewDecoderDegradesWhenSeekEndUnsupported(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 2*4096)
	dec, err := NewDecoder(seekEndUnsupported{bytes.NewReader(data)})
	if err != nil {
		t.Fatalf("NewDecoder should degrade to forward-only, not fail: %v", err)
	}
	// Seeking is unsupported on a source whose length we could not measure.
	if _, err := dec.SeekToSample(100); !errors.Is(err, flac.ErrSeekUnsupported) {
		t.Fatalf("SeekToSample err = %v, want ErrSeekUnsupported", err)
	}
	// Forward decode still reads the whole stream cleanly.
	if _, err := io.Copy(io.Discard, dec); err != nil {
		t.Fatalf("forward decode after degrade: %v", err)
	}
}

// encodeRampNonSeekable encodes the ramp to a plain io.Writer (not an io.WriteSeeker),
// so the encoder cannot patch STREAMINFO and leaves the max block size at 0 (unknown).
func encodeRampNonSeekable(t *testing.T, channels, bps, samples int) []byte {
	t.Helper()
	var buf bytes.Buffer // io.Writer but NOT io.WriteSeeker
	enc, err := NewEncoder(&buf, Config{SampleRate: 44100, Channels: channels, BitDepth: bps, CompressionLevel: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(rawRamp(channels, bps, samples)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSeekStreamWithoutMaxBlockSize(t *testing.T) {
	// A non-seekable encode leaves STREAMINFO max block size at 0 (unknown). Seeking from
	// a seekable source must still land sample-accurately by discovering the nominal block
	// size from the first frame.
	const ch, bps, n = 2, 16, 5*4096 + 13
	data := encodeRampNonSeekable(t, ch, bps, n)
	sm, err := meta.ReadMetadata(bitio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatal(err)
	}
	if sm.MaxBlock != 0 {
		t.Fatalf("precondition: MaxBlock = %d, want 0 (non-seekable encode)", sm.MaxBlock)
	}
	for _, tg := range []int64{0, 1, 4096, 9000} {
		dec, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		got, err := dec.SeekToSample(tg)
		if err != nil {
			t.Fatalf("seek %d: %v", tg, err)
		}
		if got != tg {
			t.Fatalf("seek %d returned %d", tg, got)
		}
		if v := readChan0(t, dec, ch, bps); v != rampValue(tg, bps) {
			t.Fatalf("seek %d: chan0 = %d, want %d", tg, v, rampValue(tg, bps))
		}
	}
}

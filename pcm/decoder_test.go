package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// minimalStream returns a one-frame FLAC stream: "fLaC" + STREAMINFO (2ch,16bps,
// 2 samples, MD5 zero so the MD5 check is skipped) + one frame (built by the frame
// test helper, duplicated here as raw bytes via buildStream).
func minimalStream(t *testing.T) []byte {
	t.Helper()
	return buildStream() // defined in pcm/testsupport_test.go
}

func TestDecoderInfoAndRead(t *testing.T) {
	d, err := NewDecoder(bytes.NewReader(minimalStream(t)))
	if err != nil {
		t.Fatal(err)
	}
	info := d.Info()
	if info.Channels != 2 || info.BitDepth != 16 {
		t.Fatalf("info=%+v", info)
	}
	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	// 2 samples * 2 channels * 2 bytes = 8 bytes, interleaved LE: L0 R0 L1 R1.
	// channel0=[100,100], channel1=[-100,-100].
	wantL := int16(100)
	wantR := int16(-100)
	if len(out) != 8 {
		t.Fatalf("len=%d want 8", len(out))
	}
	gotL := int16(uint16(out[0]) | uint16(out[1])<<8)
	gotR := int16(uint16(out[2]) | uint16(out[3])<<8)
	if gotL != wantL || gotR != wantR {
		t.Fatalf("L=%d R=%d want %d %d", gotL, gotR, wantL, wantR)
	}
}

func TestDecoderSmallReads(t *testing.T) {
	d, err := NewDecoder(bytes.NewReader(minimalStream(t)))
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	buf := make([]byte, 3) // smaller than one frame's PCM
	for {
		n, err := d.Read(buf)
		got = append(got, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 8 {
		t.Fatalf("len=%d want 8", len(got))
	}
}

func TestDecoderWriteTo(t *testing.T) {
	d, err := NewDecoder(bytes.NewReader(minimalStream(t)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	n, err := d.WriteTo(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 || buf.Len() != 8 {
		t.Fatalf("n=%d len=%d want 8", n, buf.Len())
	}
}

func TestDecoderNilReader(t *testing.T) {
	if _, err := NewDecoder(nil); err == nil {
		t.Fatal("want error on nil reader")
	}
}

// readerOnly hides any io.Seeker implementation so the source is treated as
// non-seekable.
type readerOnly struct{ r io.Reader }

func (ro readerOnly) Read(p []byte) (int, error) { return ro.r.Read(p) }

func TestSeekToSampleUnsupported(t *testing.T) {
	d, err := NewDecoder(readerOnly{bytes.NewReader(minimalStream(t))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SeekToSample(0); !errors.Is(err, flac.ErrSeekUnsupported) {
		t.Fatalf("non-seekable source: want ErrSeekUnsupported, got %v", err)
	}
}

func TestSeekToSampleSeekableNotImplemented(t *testing.T) {
	// A *bytes.Reader is an io.Seeker, so M2 reports the not-implemented sentinel
	// (real seeking lands in M4) rather than ErrSeekUnsupported.
	d, err := NewDecoder(bytes.NewReader(minimalStream(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SeekToSample(0); !errors.Is(err, flac.ErrNotImplemented) {
		t.Fatalf("seekable source: want ErrNotImplemented, got %v", err)
	}
}

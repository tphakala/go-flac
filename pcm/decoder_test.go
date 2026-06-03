package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/frame"
	"github.com/tphakala/go-flac/internal/meta"
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

func TestDecoderTruncatedFrameErrors(t *testing.T) {
	// Cut the single frame off mid-body. Even with a zero STREAMINFO MD5 (so the
	// MD5 check is skipped), the truncation must surface as an error rather than a
	// clean end of stream.
	full := buildStream()
	d, err := NewDecoder(bytes.NewReader(full[:len(full)-4]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(d); err == nil {
		t.Fatal("want an error decoding a truncated frame, got nil")
	}
}

func TestDecoderInterFrameTruncationDetected(t *testing.T) {
	data, samples := encodeRamp(t, 2, 16, 3*4096) // helper: returns FLAC bytes + sample count
	zeroStreamInfoMD5(data)                       // helper: clears the 16 MD5 bytes in STREAMINFO

	// Cut the stream at a frame boundary: keep metadata + first 2 frames.
	cut := truncateAtFrameBoundary(t, data, 2) // helper: bytes up to the start of frame #2
	dec, err := NewDecoder(bytes.NewReader(cut))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, dec)
	if !errors.Is(err, flac.ErrTruncatedStream) {
		t.Fatalf("err = %v, want ErrTruncatedStream", err)
	}
	_ = samples
}

func TestDecoderMidFrameTruncationDetected(t *testing.T) {
	data, _ := encodeRamp(t, 1, 16, 2*4096)
	zeroStreamInfoMD5(data)
	cut := data[:len(data)-5] // chop into the last frame's body
	dec, err := NewDecoder(bytes.NewReader(cut))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, dec)
	if !errors.Is(err, flac.ErrTruncatedStream) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want ErrTruncatedStream wrapping io.ErrUnexpectedEOF", err)
	}
}

func TestDecoderCleanReadUnaffected(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 2*4096) // seekable encode keeps a valid MD5
	dec, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, dec); err != nil {
		t.Fatalf("clean read err = %v", err)
	}
}

// encodeRamp encodes a position-encoded ramp (sample value == inter-channel index,
// per channel offset by channel index) and returns the FLAC bytes and sample count.
func encodeRamp(t *testing.T, channels, bps, samples int) (flacBytes []byte, sampleCount int) {
	t.Helper()
	var sb seekBuffer
	enc, err := NewEncoder(&sb, Config{SampleRate: 44100, Channels: channels, BitDepth: bps, CompressionLevel: 2})
	if err != nil {
		t.Fatal(err)
	}
	bytesPS := (bps + 7) / 8
	pcm := make([]byte, samples*channels*bytesPS)
	idx := 0
	for s := range samples {
		for c := range channels {
			v := uint32(int32(s + c))
			for b := range bytesPS {
				pcm[idx] = byte(v >> (uint(b) * 8))
				idx++
			}
		}
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return sb.Bytes(), samples
}

// zeroStreamInfoMD5 clears the 16-byte MD5 field of STREAMINFO (last 16 bytes of the
// 34-byte body at StreamInfoBodyOffset), forcing the zero-MD5 code path.
func zeroStreamInfoMD5(data []byte) {
	off := 8 + 34 - 16 // StreamInfoBodyOffset + body - MD5 length
	for i := range 16 {
		data[off+i] = 0
	}
}

// truncateAtFrameBoundary returns data up to the start of the nth audio frame by
// decoding n frames and recording the byte offset. It reuses frame.FindNextFrame to
// locate frame starts after the metadata section.
func truncateAtFrameBoundary(t *testing.T, data []byte, n int) []byte {
	t.Helper()
	br := bitio.NewReader(bytes.NewReader(data))
	sm, err := meta.ReadMetadata(br)
	if err != nil {
		t.Fatal(err)
	}
	audio := int(br.BytesRead())
	off := audio
	var fr frame.Frame
	for i := range n {
		s, consumed, res := frame.FindNextFrame(data[off:], sm.Info, &fr)
		if res != frame.FrameFound {
			t.Fatalf("frame %d not found: %v", i, res)
		}
		off += s + consumed
	}
	return data[:off]
}

package pcm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// genPCM builds interleaved little-endian PCM with a mildly compressible signal.
func genPCM(cfg Config, nSamples int) []byte {
	bytesPS := (cfg.BitDepth + 7) / 8
	lim := int64(1) << (cfg.BitDepth - 1)
	out := make([]byte, 0, nSamples*cfg.Channels*bytesPS)
	x := int64(2463534242)
	for i := range nSamples {
		for c := range cfg.Channels {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			// mostly a ramp (compressible) plus a little noise
			v := int64(i%1000-500)*(int64(c)+1) + (x % 17)
			if v >= lim {
				v = lim - 1
			}
			if v < -lim {
				v = -lim
			}
			u := uint64(v)
			for b := range bytesPS {
				out = append(out, byte(u>>(uint(b)*8)))
			}
		}
	}
	return out
}

func encodeToFile(t *testing.T, cfg Config, pcmBytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.flac")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncoder(f, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	// Write in odd-sized chunks to exercise mid-sample boundaries.
	for off := 0; off < len(pcmBytes); off += 777 {
		end := min(off+777, len(pcmBytes))
		if _, err := enc.Write(pcmBytes[off:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeAll(t *testing.T, r interface {
	Read([]byte) (int, error)
}) (si flac.StreamInfo, pcm []byte) {
	t.Helper()
	d, err := NewDecoder(r)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil { // reaching clean EOF runs the MD5 check
		t.Fatalf("WriteTo: %v", err)
	}
	return d.Info(), buf.Bytes()
}

func TestEncodeRoundTripSeekable(t *testing.T) {
	cfgs := []Config{
		{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5},
		{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0},
		{SampleRate: 48000, BitDepth: 24, Channels: 2, CompressionLevel: 8},
		{SampleRate: 8000, BitDepth: 8, Channels: 1, CompressionLevel: 2},
	}
	for _, cfg := range cfgs {
		pcmBytes := genPCM(cfg, 4096*2+1234) // two full frames + a short final frame
		path := encodeToFile(t, cfg, pcmBytes)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		si, got := decodeAll(t, f)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pcmBytes) {
			t.Fatalf("cfg %+v: PCM round trip mismatch (got %d bytes, want %d)", cfg, len(got), len(pcmBytes))
		}
		var zero [16]byte
		if si.MD5 == zero {
			t.Errorf("cfg %+v: seekable stream should have a non-zero MD5", cfg)
		}
		bytesPS := (cfg.BitDepth + 7) / 8
		wantTotal := uint64(len(pcmBytes) / (cfg.Channels * bytesPS))
		if si.TotalSamples != wantTotal {
			t.Errorf("cfg %+v: TotalSamples=%d, want %d", cfg, si.TotalSamples, wantTotal)
		}
	}
}

func TestEncodeAllLevelsRoundTrip(t *testing.T) {
	for level := 0; level <= 8; level++ {
		cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: level}
		pcmBytes := genPCM(cfg, 5000)
		path := encodeToFile(t, cfg, pcmBytes)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, got := decodeAll(t, f)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pcmBytes) {
			t.Fatalf("level %d: round trip mismatch", level)
		}
	}
}

func TestEncodeNonSeekableUnknownSentinels(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096+10)
	var buf bytes.Buffer // *bytes.Buffer is not an io.Seeker
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(pcmBytes); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	si, got := decodeAll(t, bytes.NewReader(buf.Bytes()))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("non-seekable round trip mismatch")
	}
	var zero [16]byte
	if si.MD5 != zero || si.TotalSamples != 0 {
		t.Fatalf("non-seekable expected unknown sentinels, got MD5=%x total=%d", si.MD5, si.TotalSamples)
	}
}

// TestEncoderCarryBounded asserts the join buffer stays bounded at single-block
// scale regardless of any single Write size. The old Write concatenated all of a
// large Write into e.carry and retained that backing array via carry[:0] reuse.
func TestEncoderCarryBounded(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0}
	enc, err := NewEncoder(io.Discard, cfg)
	if err != nil {
		t.Fatal(err)
	}
	blockBytes := encoderBlockSize * enc.frameLen

	// A small write leaves a partial-block remainder, so the next Write takes the
	// leftover-join path.
	if _, err := enc.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	// A single very large Write. carry must not grow to match it.
	if _, err := enc.Write(make([]byte, 64*blockBytes)); err != nil {
		t.Fatal(err)
	}
	if cap(enc.carry) > 4*blockBytes {
		t.Fatalf("cap(e.carry) = %d, want <= %d (carry must stay bounded at single-block scale, not grow to the Write size)",
			cap(enc.carry), 4*blockBytes)
	}
}

// TestEncoderWriteBoundaryChunks drives Write through every boundary the bounded
// rewrite cares about (sub-need accumulate, exact-block completion, empty write,
// direct full block with no leftover) and asserts a byte-exact round trip.
func TestEncoderWriteBoundaryChunks(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0}
	pcmBytes := genPCM(cfg, 4096*3) // three full blocks of mono 16-bit PCM
	bytesPS := (cfg.BitDepth + 7) / 8
	blockBytes := encoderBlockSize * bytesPS * cfg.Channels

	path := filepath.Join(t.TempDir(), "out.flac")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncoder(f, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	//   100              -> leftover < need (accumulate only, no block emitted)
	//   blockBytes-100   -> exactly completes block 1 (exact-block completion)
	//   0                -> empty write
	//   blockBytes       -> a full block with no prior leftover (direct path)
	//   rest             -> remainder
	sizes := []int{100, blockBytes - 100, 0, blockBytes}
	off := 0
	for _, s := range sizes {
		if _, err := enc.Write(pcmBytes[off : off+s]); err != nil {
			t.Fatalf("Write(%d): %v", s, err)
		}
		off += s
	}
	if _, err := enc.Write(pcmBytes[off:]); err != nil {
		t.Fatalf("Write(rest): %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	f2, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f2.Close() }()
	_, got := decodeAll(t, f2)
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("boundary-chunk round trip mismatch (got %d bytes, want %d)", len(got), len(pcmBytes))
	}
}

// armedFailWriter succeeds until armed, then allows `allow` more writes before
// failing every subsequent write. It lets a test drive a sink failure on a chosen
// block boundary without depending on how many writes the stream header emits.
type armedFailWriter struct {
	armed bool
	allow int
}

func (w *armedFailWriter) Write(p []byte) (int, error) {
	if w.armed {
		if w.allow <= 0 {
			return 0, errors.New("sink failure")
		}
		w.allow--
	}
	return len(p), nil
}

// TestEncoderWritePartialCountOnError checks the io.Writer contract: when a block
// fails mid-Write after earlier blocks were already handed to the sink, Write must
// report the bytes of p it consumed, not 0.
func TestEncoderWritePartialCountOnError(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0}
	w := &armedFailWriter{}
	enc, err := NewEncoder(w, cfg) // header writes happen while unarmed
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	blockBytes := encoderBlockSize * enc.frameLen

	w.armed, w.allow = true, 1 // allow exactly one block frame, then fail
	n, werr := enc.Write(make([]byte, 2*blockBytes))
	if werr == nil {
		t.Fatal("expected sink failure error, got nil")
	}
	if n != blockBytes {
		t.Fatalf("Write returned n=%d on partial failure, want %d (one block consumed)", n, blockBytes)
	}
}

func TestNewEncoderRejectsBadConfig(t *testing.T) {
	bad := []Config{
		{SampleRate: 0, BitDepth: 16, Channels: 2},
		{SampleRate: 700000, BitDepth: 16, Channels: 2}, // above the FLAC 655350 max
		{SampleRate: 44100, BitDepth: 3, Channels: 2},   // below 4
		{SampleRate: 44100, BitDepth: 33, Channels: 2},  // above 32 (FLAC max)
		{SampleRate: 44100, BitDepth: 16, Channels: 0},
		{SampleRate: 44100, BitDepth: 16, Channels: 9},
	}
	for _, cfg := range bad {
		if _, err := NewEncoder(&bytes.Buffer{}, cfg); err == nil {
			t.Errorf("NewEncoder(%+v) = nil error, want error", cfg)
		}
	}
	if _, err := NewEncoder(nil, Config{SampleRate: 44100, BitDepth: 16, Channels: 2}); err == nil {
		t.Error("NewEncoder(nil writer) = nil error, want error")
	}
}

func TestEncoderWriteAfterCloseErrors(t *testing.T) {
	enc, err := NewEncoder(&bytes.Buffer{}, Config{SampleRate: 44100, BitDepth: 16, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	_, werr := enc.Write([]byte{0, 0, 0, 0})
	if !errors.Is(werr, flac.ErrEncoderClosed) {
		t.Fatalf("Write after Close = %v, want ErrEncoderClosed", werr)
	}
}

// TestEncodeOutOfRangePCMNoPanic feeds non-byte-aligned-depth PCM whose samples
// exceed the declared bit depth so that every sample shares more trailing zero
// bits than the bit depth (wasted >= bps). Before the wasted clamp in
// planSubframe this underflowed bps-wasted to a huge shift width and hung; the
// encode must now complete and the stream must stay structurally decodable.
func TestEncodeOutOfRangePCMNoPanic(t *testing.T) {
	const bps = 12 // bytesPS 2, so 16-bit input samples can exceed the 12-bit range
	cfg := Config{SampleRate: 44100, BitDepth: bps, Channels: 1, CompressionLevel: 5}
	// Samples are multiples of 2^13 (8192, 16384, 24576): non-constant, all with
	// at least 13 trailing zeros, i.e. wasted (13) > bps (12).
	vals := []int16{8192, 16384, 24576}
	raw := make([]byte, 0, 600)
	for i := range 300 {
		v := uint16(vals[i%len(vals)])
		raw = append(raw, byte(v), byte(v>>8))
	}

	var buf bytes.Buffer // non-seekable: STREAMINFO MD5 stays the unknown sentinel
	enc, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(raw); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The stream is lossy for this out-of-range input, but it must decode without
	// a panic or error (the unknown MD5 means the decoder skips the MD5 check).
	if _, got := decodeAll(t, bytes.NewReader(buf.Bytes())); len(got) == 0 {
		t.Fatal("decoded zero bytes from out-of-range encode")
	}
}

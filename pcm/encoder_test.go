package pcm

import (
	"bytes"
	"errors"
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

func TestNewEncoderRejectsBadConfig(t *testing.T) {
	bad := []Config{
		{SampleRate: 0, BitDepth: 16, Channels: 2},
		{SampleRate: 44100, BitDepth: 3, Channels: 2},  // below 4
		{SampleRate: 44100, BitDepth: 32, Channels: 2}, // above 24 (M3 scope)
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

package pcm

import (
	"bytes"
	"sync"
	"testing"
)

// TestEncodeInterleavedRoundTripSeekable encodes a complete in-memory PCM buffer
// in one call to a seekable sink and checks the stream decodes back exactly, that
// STREAMINFO totals/MD5 are finalized, and that the bytes match the manual
// NewEncoder/Write/Close sequence (the helper must reproduce that finalization).
func TestEncodeInterleavedRoundTripSeekable(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096*2+321)

	sb := &seekBuffer{}
	if err := EncodeInterleaved(sb, cfg, pcmBytes); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}

	si, got := decodeAll(t, bytes.NewReader(sb.Bytes()))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("round trip mismatch (got %d bytes, want %d)", len(got), len(pcmBytes))
	}
	var zero [16]byte
	if si.MD5 == zero {
		t.Error("seekable EncodeInterleaved should finalize a non-zero MD5")
	}
	bytesPS := (cfg.BitDepth + 7) / 8
	wantTotal := uint64(len(pcmBytes) / (cfg.Channels * bytesPS))
	if si.TotalSamples != wantTotal {
		t.Errorf("TotalSamples=%d, want %d", si.TotalSamples, wantTotal)
	}

	// Byte-identical to the manual encode dance.
	manual := &seekBuffer{}
	enc, err := NewEncoder(manual, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcmBytes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(sb.Bytes(), manual.Bytes()) {
		t.Fatal("EncodeInterleaved output differs from manual NewEncoder/Write/Close")
	}
}

// TestEncodeInterleavedPlainWriter checks the helper still produces a valid,
// decodable stream on a non-seekable sink, leaving totals/MD5 at their spec-legal
// unknown sentinels.
func TestEncodeInterleavedPlainWriter(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 2}
	pcmBytes := genPCM(cfg, 3000)

	var buf bytes.Buffer // *bytes.Buffer is not an io.Seeker
	if err := EncodeInterleaved(&buf, cfg, pcmBytes); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}

	si, got := decodeAll(t, bytes.NewReader(buf.Bytes()))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("round trip mismatch (got %d bytes, want %d)", len(got), len(pcmBytes))
	}
	var zero [16]byte
	if si.MD5 != zero || si.TotalSamples != 0 {
		t.Errorf("plain writer expected unknown sentinels, got MD5=%x total=%d", si.MD5, si.TotalSamples)
	}
}

// TestEncodeInterleavedErrors checks that the helper surfaces config validation
// errors and the trailing-partial-sample error (which arises in Close).
func TestEncodeInterleavedErrors(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2}
	good := genPCM(cfg, 100)

	if err := EncodeInterleaved(&bytes.Buffer{}, Config{}, good); err == nil {
		t.Error("invalid config = nil error, want error")
	}
	if err := EncodeInterleaved(nil, cfg, good); err == nil {
		t.Error("nil writer = nil error, want error")
	}
	// One stray byte: the buffer no longer ends on a whole inter-channel sample.
	stray := append(bytes.Clone(good), 0x01)
	if err := EncodeInterleaved(&bytes.Buffer{}, cfg, stray); err == nil {
		t.Error("trailing partial sample = nil error, want error")
	}
}

// TestEncodeInterleavedSeekTable runs the pooled one-shot path with a SEEKTABLE
// config so the placeholder-reserve and Close patch-back are exercised through
// Reset/pool reuse, not just the plain NewEncoder path.
func TestEncodeInterleavedSeekTable(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5, SeekTableInterval: 4096}
	pcmBytes := genPCM(cfg, 4096*3+77)

	sb := &seekBuffer{}
	if err := EncodeInterleaved(sb, cfg, pcmBytes); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}

	si, got := decodeAll(t, bytes.NewReader(sb.Bytes()))
	if !bytes.Equal(got, pcmBytes) {
		t.Fatalf("round trip mismatch (got %d bytes, want %d)", len(got), len(pcmBytes))
	}
	bytesPS := (cfg.BitDepth + 7) / 8
	wantTotal := uint64(len(pcmBytes) / (cfg.Channels * bytesPS))
	if si.TotalSamples != wantTotal {
		t.Errorf("TotalSamples=%d, want %d", si.TotalSamples, wantTotal)
	}
}

// TestEncodeInterleavedPoolReuse calls the helper many times back to back with
// varying stream lengths; each call must yield an independent, correct stream,
// proving the internal encoder pool plus Reset do not bleed state between calls.
func TestEncodeInterleavedPoolReuse(t *testing.T) {
	cfg := Config{SampleRate: 22050, BitDepth: 16, Channels: 1, CompressionLevel: 5}
	for i := 1; i <= 8; i++ {
		pcmBytes := genPCM(cfg, 1000*i)
		sb := &seekBuffer{}
		if err := EncodeInterleaved(sb, cfg, pcmBytes); err != nil {
			t.Fatalf("iter %d: EncodeInterleaved: %v", i, err)
		}
		if _, got := decodeAll(t, bytes.NewReader(sb.Bytes())); !bytes.Equal(got, pcmBytes) {
			t.Fatalf("iter %d: round trip mismatch", i)
		}
	}
}

// TestEncodeInterleavedPoolShapeChange alternates the config shape across calls so
// a pooled encoder is repeatedly handed back for a different channel count and LPC
// order, exercising the realloc-on-shape-change branch of Reset through the pool.
// Each stream must still decode back exactly.
func TestEncodeInterleavedPoolShapeChange(t *testing.T) {
	shapes := []Config{
		{SampleRate: 44100, BitDepth: 16, Channels: 1, CompressionLevel: 0},
		{SampleRate: 48000, BitDepth: 24, Channels: 2, CompressionLevel: 8},
		{SampleRate: 22050, BitDepth: 16, Channels: 2, CompressionLevel: 5},
	}
	for i := range 9 {
		cfg := shapes[i%len(shapes)]
		pcmBytes := genPCM(cfg, 4096+101*i)
		sb := &seekBuffer{}
		if err := EncodeInterleaved(sb, cfg, pcmBytes); err != nil {
			t.Fatalf("iter %d (%+v): EncodeInterleaved: %v", i, cfg, err)
		}
		if _, got := decodeAll(t, bytes.NewReader(sb.Bytes())); !bytes.Equal(got, pcmBytes) {
			t.Fatalf("iter %d (%+v): round trip mismatch", i, cfg)
		}
	}
}

// TestEncodeInterleavedConcurrent hammers the helper from many goroutines so the
// race detector validates that the internal sync.Pool usage is concurrency-safe
// and that no encoder is shared mid-stream. Decoding runs after the goroutines
// join (t.Fatalf is only safe on the test goroutine).
func TestEncodeInterleavedConcurrent(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcmBytes := genPCM(cfg, 4096+200)

	const n = 16
	results := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sb := &seekBuffer{}
			errs[i] = EncodeInterleaved(sb, cfg, pcmBytes)
			results[i] = sb.Bytes()
		}(i)
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if _, got := decodeAll(t, bytes.NewReader(results[i])); !bytes.Equal(got, pcmBytes) {
			t.Fatalf("goroutine %d: round trip mismatch", i)
		}
	}
}

package pcm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// makeStream encodes a deterministic multi-frame FLAC stream of the given shape,
// so the stream carries a finalized nonzero STREAMINFO MD5 (EncodeInterleaved
// hashes the input), giving the decoder's MD5 verification real teeth.
func makeStream(t *testing.T, cfg Config, samples int) []byte {
	t.Helper()
	bytesPS := (cfg.BitDepth + 7) / 8
	pcm := make([]byte, samples*cfg.Channels*bytesPS)
	// A per-shape seed so different streams have different audio and different MD5s.
	seed := uint32(cfg.SampleRate*131 + cfg.Channels*17 + cfg.BitDepth)
	for i := range pcm {
		seed = seed*1664525 + 1013904223
		pcm[i] = byte(seed >> 24)
	}
	var buf bytes.Buffer
	if err := EncodeInterleaved(&buf, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved(%dch/%dbit): %v", cfg.Channels, cfg.BitDepth, err)
	}
	return buf.Bytes()
}

// corruptMD5 returns a copy of stream with one byte of the STREAMINFO MD5 flipped,
// so a full decode that verifies MD5 fails with flac.ErrMD5Mismatch. Layout:
// "fLaC" (4) + block header (4) + STREAMINFO body (34), MD5 is the body's last 16.
func corruptMD5(stream []byte) []byte {
	out := bytes.Clone(stream)
	md5Off := 4 + 4 + (34 - 16) // = 26; MD5 occupies out[26:42]
	out[md5Off+7] ^= 0xff
	return out
}

func sourceFor(stream []byte, seekable bool) io.Reader {
	if seekable {
		return bytes.NewReader(stream)
	}
	return readerOnly{bytes.NewReader(stream)}
}

var resetShapes = map[string]Config{
	"stereo16": {SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5},
	"mono24":   {SampleRate: 48000, BitDepth: 24, Channels: 1, CompressionLevel: 8},
	"eight16":  {SampleRate: 44100, BitDepth: 16, Channels: 8, CompressionLevel: 3},
}

// TestDecoderResetMatchesFresh is the core equivalence guard: after decoding one
// stream, Reset to another must produce output and Info() byte-identical to a
// fresh NewDecoder on that stream, for every ordered pair of shapes (exercising
// buffer growth and shrink) and both seekable and non-seekable sources.
func TestDecoderResetMatchesFresh(t *testing.T) {
	names := []string{"stereo16", "mono24", "eight16"}
	streams := make(map[string][]byte, len(names))
	for _, n := range names {
		streams[n] = makeStream(t, resetShapes[n], 9000) // ~2-3 frames at block 4096
	}

	for _, a := range names {
		for _, b := range names {
			for _, seekable := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s_then_%s/seekable=%v", a, b, seekable), func(t *testing.T) {
					d, err := NewDecoder(sourceFor(streams[a], seekable))
					if err != nil {
						t.Fatalf("NewDecoder(A): %v", err)
					}
					if _, err := io.Copy(io.Discard, d); err != nil {
						t.Fatalf("decode A: %v", err)
					}

					if err := d.Reset(sourceFor(streams[b], seekable)); err != nil {
						t.Fatalf("Reset(B): %v", err)
					}
					gotInfo := d.Info()
					var got bytes.Buffer
					if _, err := io.Copy(&got, d); err != nil {
						t.Fatalf("decode B after Reset: %v", err)
					}

					wantInfo, want := decodeAll(t, sourceFor(streams[b], seekable))

					if !bytes.Equal(got.Bytes(), want) {
						t.Fatalf("PCM after Reset differs from fresh decode: %d vs %d bytes", got.Len(), len(want))
					}
					if gotInfo != wantInfo {
						t.Fatalf("Info() after Reset = %+v, want %+v", gotInfo, wantInfo)
					}
				})
			}
		}
	}
}

// TestDecoderResetZeroValue pins the sync.Pool entry point: a zero-value Decoder
// (never constructed via NewDecoder) must Reset and decode identically to a fresh
// decoder.
func TestDecoderResetZeroValue(t *testing.T) {
	stream := makeStream(t, resetShapes["stereo16"], 9000)
	var d Decoder
	if err := d.Reset(bytes.NewReader(stream)); err != nil {
		t.Fatalf("zero-value Reset: %v", err)
	}
	var got bytes.Buffer
	if _, err := io.Copy(&got, &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, want := decodeAll(t, bytes.NewReader(stream)); !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("zero-value Reset decode differs from fresh: %d vs %d bytes", got.Len(), len(want))
	}
}

// TestDecoderResetMidStream proves md5.Reset() is applied: a Reset partway through
// stream A must not carry A's partial hash into B, so decoding a correct-MD5 B to
// EOF verifies cleanly rather than raising a false ErrMD5Mismatch.
func TestDecoderResetMidStream(t *testing.T) {
	a := makeStream(t, resetShapes["mono24"], 12000)
	b := makeStream(t, resetShapes["stereo16"], 9000)

	d, err := NewDecoder(bytes.NewReader(a))
	if err != nil {
		t.Fatalf("NewDecoder(A): %v", err)
	}
	// Consume part of A so the MD5 hash is partially fed and pending is non-empty.
	if _, err := io.CopyN(io.Discard, d, 500); err != nil {
		t.Fatalf("partial decode A: %v", err)
	}

	if err := d.Reset(bytes.NewReader(b)); err != nil {
		t.Fatalf("Reset(B): %v", err)
	}
	var got bytes.Buffer
	if _, err := io.Copy(&got, d); err != nil {
		t.Fatalf("decode B after mid-stream Reset (stale MD5 would surface here): %v", err)
	}
	if _, want := decodeAll(t, bytes.NewReader(b)); !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("mid-stream Reset decode differs from fresh: %d vs %d bytes", got.Len(), len(want))
	}
}

// TestDecoderResetAfterSeek proves seeked is cleared: a Reset following a seek must
// re-enable MD5 verification on the new stream, so a corrupted-MD5 B fails with
// ErrMD5Mismatch instead of silently skipping the check.
func TestDecoderResetAfterSeek(t *testing.T) {
	a := makeStream(t, resetShapes["stereo16"], 12000)
	bBad := corruptMD5(makeStream(t, resetShapes["mono24"], 9000))

	// Sanity: a fresh decode of the corrupted stream must fail verification, else
	// the negative test below proves nothing.
	if _, err := io.Copy(io.Discard, mustDecoder(t, bytes.NewReader(bBad))); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("fresh decode of corrupted stream: err = %v, want ErrMD5Mismatch", err)
	}

	d, err := NewDecoder(bytes.NewReader(a))
	if err != nil {
		t.Fatalf("NewDecoder(A): %v", err)
	}
	if _, err := d.SeekToSample(2048); err != nil {
		t.Fatalf("SeekToSample(A): %v", err)
	}

	if err := d.Reset(bytes.NewReader(bBad)); err != nil {
		t.Fatalf("Reset(B): %v", err)
	}
	if _, err := io.Copy(io.Discard, d); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("decode after Reset-following-seek: err = %v, want ErrMD5Mismatch (seeked leaked, verification skipped)", err)
	}
}

// TestDecoderResetSeekabilityFlip proves the seek-only fields are cleared: a
// seekable-then-non-seekable Reset must drop seekability (no stale rs/seekPoints),
// and a later seekable Reset must restore it.
func TestDecoderResetSeekabilityFlip(t *testing.T) {
	a := makeStream(t, resetShapes["stereo16"], 9000)
	b := makeStream(t, resetShapes["mono24"], 9000)
	c := makeStream(t, resetShapes["eight16"], 9000)

	d, err := NewDecoder(bytes.NewReader(a)) // seekable
	if err != nil {
		t.Fatalf("NewDecoder(A): %v", err)
	}
	if _, err := d.SeekToSample(1024); err != nil {
		t.Fatalf("seek A: %v", err)
	}

	// Reset to a non-seekable source: SeekToSample must now be unsupported.
	if err := d.Reset(readerOnly{bytes.NewReader(b)}); err != nil {
		t.Fatalf("Reset(B non-seekable): %v", err)
	}
	if _, err := d.SeekToSample(0); !errors.Is(err, flac.ErrSeekUnsupported) {
		t.Fatalf("seek after non-seekable Reset: err = %v, want ErrSeekUnsupported (stale seekable leaked)", err)
	}

	// Reset back to a seekable source: seeking must work again.
	if err := d.Reset(bytes.NewReader(c)); err != nil {
		t.Fatalf("Reset(C seekable): %v", err)
	}
	if _, err := d.SeekToSample(1024); err != nil {
		t.Fatalf("seek after seekable Reset: %v (seekability not restored)", err)
	}
}

// TestDecoderResetPoisonedRecoverable pins the failure contract: a Reset onto a bad
// stream returns an error and poisons the decoder (Read returns the sticky error,
// Info() is zeroed), and a subsequent Reset onto a good stream fully recovers it.
func TestDecoderResetPoisonedRecoverable(t *testing.T) {
	good := makeStream(t, resetShapes["stereo16"], 9000)
	d, err := NewDecoder(bytes.NewReader(good))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		t.Fatalf("decode good: %v", err)
	}

	// Reset onto garbage: no FLAC header, so metadata parse fails.
	rerr := d.Reset(bytes.NewReader([]byte("not a flac stream at all, just bytes")))
	if rerr == nil {
		t.Fatal("Reset onto garbage: want error, got nil")
	}
	// The decoder is poisoned: Read surfaces the same sticky error, Info() is zero.
	if _, err := d.Read(make([]byte, 16)); !errors.Is(err, rerr) {
		t.Fatalf("Read after failed Reset: err = %v, want the sticky Reset error %v", err, rerr)
	}
	if info := d.Info(); info != (flac.StreamInfo{}) {
		t.Fatalf("Info() after failed Reset = %+v, want zero", info)
	}

	// A fresh Reset onto a good stream recovers the decoder completely.
	if err := d.Reset(bytes.NewReader(good)); err != nil {
		t.Fatalf("recovery Reset: %v", err)
	}
	var got bytes.Buffer
	if _, err := io.Copy(&got, d); err != nil {
		t.Fatalf("decode after recovery: %v", err)
	}
	if _, want := decodeAll(t, bytes.NewReader(good)); !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("recovered decode differs from fresh: %d vs %d bytes", got.Len(), len(want))
	}
}

// TestDecoderResetAfterFailedSeek covers the br == nil branch: a decoder driven
// into the hard-failed seek state (which nils br) must still Reset and decode.
func TestDecoderResetAfterFailedSeek(t *testing.T) {
	src := newFailingSource(makeStream(t, resetShapes["stereo16"], 12000))
	d, err := NewDecoder(src)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	src.arm(0) // fail the next source read so the seek probe hard-fails
	if _, err := d.SeekToSample(4096); err == nil {
		t.Fatal("armed seek: want failure, got nil")
	}
	src.disarm()

	good := makeStream(t, resetShapes["mono24"], 9000)
	if err := d.Reset(bytes.NewReader(good)); err != nil {
		t.Fatalf("Reset after failed seek (br was nil): %v", err)
	}
	var got bytes.Buffer
	if _, err := io.Copy(&got, d); err != nil {
		t.Fatalf("decode after Reset-post-failed-seek: %v", err)
	}
	if _, want := decodeAll(t, bytes.NewReader(good)); !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("decode differs from fresh: %d vs %d bytes", got.Len(), len(want))
	}
}

func TestDecoderResetNilReader(t *testing.T) {
	stream := makeStream(t, resetShapes["stereo16"], 9000)
	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := d.Reset(nil); err == nil {
		t.Fatal("Reset(nil): want error, got nil")
	}
	// A nil reader is rejected before any state changes, so a following Reset onto a
	// good stream works.
	if err := d.Reset(bytes.NewReader(stream)); err != nil {
		t.Fatalf("Reset after nil: %v", err)
	}
}

// TestDecodeResetReuseAllocs is the headline allocation guard for issue #24: after
// the decoder exists, rebinding it with Reset must not re-allocate the large
// per-decoder setup buffers (the bitio read buffer, channel buffers, packed-PCM
// buffer, decode scratch). AllocsPerRun warms up once, so the first construction's
// allocations are excluded; the remaining per-Reset allocs are only the small
// header/MD5-finalization churn, far below the ~322 a fresh NewDecoder pays.
func TestDecodeResetReuseAllocs(t *testing.T) {
	stream := makeStream(t, resetShapes["stereo16"], 12000)
	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		t.Fatalf("warm decode: %v", err)
	}
	sr := bytes.NewReader(stream)

	// A reused decoder re-parses only the metadata header and finalizes MD5 per
	// stream, so its allocation count stays tiny; a fresh NewDecoder of this stream
	// costs ~322 allocs/op. The bound sits well below that so any regression that
	// drops the buffer reuse reddens here, with margin for minor churn.
	const maxAllocs = 24
	allocs := testing.AllocsPerRun(30, func() {
		sr.Reset(stream) // rewind without allocating a new reader
		if err := d.Reset(sr); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if _, err := io.Copy(io.Discard, d); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
	t.Logf("Reset-and-decode allocates %.1f allocs/op", allocs)
	if allocs > maxAllocs {
		t.Fatalf("Reset-and-decode allocates %.0f/op (want <= %d); the buffer reuse likely regressed", allocs, maxAllocs)
	}
}

// TestDecoderPoolSmoke exercises the documented sync.Pool pattern concurrently, so
// the -race build validates that a pooled, Reset-reused decoder carries no shared
// state across goroutines.
func TestDecoderPoolSmoke(t *testing.T) {
	stream := makeStream(t, resetShapes["stereo16"], 9000)
	_, want := decodeAll(t, bytes.NewReader(stream))

	pool := sync.Pool{New: func() any { return new(Decoder) }}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				d := pool.Get().(*Decoder)
				if err := d.Reset(bytes.NewReader(stream)); err != nil {
					t.Errorf("pooled Reset: %v", err)
					pool.Put(d)
					return
				}
				var got bytes.Buffer
				if _, err := io.Copy(&got, d); err != nil {
					t.Errorf("pooled decode: %v", err)
					pool.Put(d)
					return
				}
				if !bytes.Equal(got.Bytes(), want) {
					t.Errorf("pooled decode mismatch: %d vs %d bytes", got.Len(), len(want))
				}
				pool.Put(d)
			}
		})
	}
	wg.Wait()
}

func mustDecoder(t *testing.T, r io.Reader) *Decoder {
	t.Helper()
	d, err := NewDecoder(r)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return d
}

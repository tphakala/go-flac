package pcm

import (
	"bytes"
	"crypto/md5"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// md5FieldStart is the byte offset of the STREAMINFO MD5 within an encoded stream:
// "fLaC" (4) + metadata block header (4) + STREAMINFO body (34), MD5 the last 16.
const md5FieldStart = 4 + 4 + (34 - 16) // 26

func md5Field(stream []byte) []byte { return stream[md5FieldStart : md5FieldStart+16] }

// encodeStreaming encodes pcm through the streaming Encoder to a seekable in-memory
// sink (so STREAMINFO is patched at Close) and returns the finished bytes.
func encodeStreaming(t *testing.T, cfg Config, pcm []byte) []byte {
	t.Helper()
	var sink seekBuffer
	enc, err := NewEncoder(&sink, cfg)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return sink.Bytes()
}

// TestEncoderSkipMD5 pins that Config.SkipMD5 makes the streaming Encoder write the
// all-zero STREAMINFO MD5 while leaving every other byte identical to a normal encode,
// and that the default path still writes md5(input). Sabotage: dropping `|| cfg.SkipMD5`
// from Encoder.init makes the encoder hash and patch a non-zero MD5, reddening this.
func TestEncoderSkipMD5(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096*2+1500) // two full frames + a short final frame

	cfgSkip := cfg
	cfgSkip.SkipMD5 = true

	full := encodeStreaming(t, cfg, pcm)
	skip := encodeStreaming(t, cfgSkip, pcm)

	if len(full) != len(skip) {
		t.Fatalf("SkipMD5 changed stream length: %d vs %d", len(skip), len(full))
	}
	// Byte-identical outside the 16-byte MD5 field.
	if !bytes.Equal(full[:md5FieldStart], skip[:md5FieldStart]) ||
		!bytes.Equal(full[md5FieldStart+16:], skip[md5FieldStart+16:]) {
		t.Fatal("streams differ outside the MD5 field; SkipMD5 must change only the MD5")
	}
	if want := md5.Sum(pcm); !bytes.Equal(md5Field(full), want[:]) {
		t.Errorf("default MD5 = %x, want md5(input) = %x", md5Field(full), want)
	}
	var zero [16]byte
	if !bytes.Equal(md5Field(skip), zero[:]) {
		t.Errorf("SkipMD5 MD5 = %x, want all zeros", md5Field(skip))
	}

	// The zero MD5 reads back as the unknown sentinel and the audio round-trips.
	si, samples := decodeAll(t, bytes.NewReader(skip))
	if si.MD5 != zero {
		t.Errorf("decoded SkipMD5 stream MD5 = %x, want zero", si.MD5)
	}
	if !bytes.Equal(samples, pcm) {
		t.Errorf("SkipMD5 round-trip: decoded PCM differs from input (%d vs %d bytes)", len(samples), len(pcm))
	}
}

// TestEncodeInterleavedSkipMD5 pins the one-shot path: SkipMD5 leaves the MD5 zero (and
// skips the up-front md5.Sum) while total_samples is still finalized for a non-seekable
// sink; the default still writes md5(input). Sabotage: dropping the `if !cfg.SkipMD5`
// guard around the up-front hash reddens the zero-MD5 assertion.
func TestEncodeInterleavedSkipMD5(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	nSamples := 4096 + 700
	pcm := genPCM(cfg, nSamples)

	cfgSkip := cfg
	cfgSkip.SkipMD5 = true
	var skip bytes.Buffer
	if err := EncodeInterleaved(&skip, cfgSkip, pcm); err != nil {
		t.Fatalf("EncodeInterleaved(SkipMD5): %v", err)
	}
	siSkip, samples := decodeAll(t, bytes.NewReader(skip.Bytes()))
	var zero [16]byte
	if siSkip.MD5 != zero {
		t.Errorf("EncodeInterleaved SkipMD5 MD5 = %x, want zero", siSkip.MD5)
	}
	if siSkip.TotalSamples != uint64(nSamples) {
		t.Errorf("EncodeInterleaved SkipMD5 total_samples = %d, want %d", siSkip.TotalSamples, nSamples)
	}
	if !bytes.Equal(samples, pcm) {
		t.Error("EncodeInterleaved SkipMD5 round-trip: decoded PCM differs from input")
	}

	var full bytes.Buffer
	if err := EncodeInterleaved(&full, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved(default): %v", err)
	}
	siFull, _ := decodeAll(t, bytes.NewReader(full.Bytes()))
	if want := md5.Sum(pcm); siFull.MD5 != want {
		t.Errorf("EncodeInterleaved default MD5 = %x, want md5(input) = %x", siFull.MD5, want)
	}
}

// TestFrameEncoderSkipMD5 pins that FrameEncoder honors SkipMD5. Sabotage: dropping the
// `if !e.skipHash` guard in streamInfoParams reddens the zero-MD5 assertion.
func TestFrameEncoderSkipMD5(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096+512)

	encode := func(c Config) flac.StreamInfo {
		fe, err := NewFrameEncoder(c)
		if err != nil {
			t.Fatalf("NewFrameEncoder: %v", err)
		}
		if err := fe.EncodeInterleaved(pcm, func([]byte, int) error { return nil }); err != nil {
			t.Fatalf("FrameEncoder.EncodeInterleaved: %v", err)
		}
		return fe.StreamInfo()
	}

	var zero [16]byte
	cfgSkip := cfg
	cfgSkip.SkipMD5 = true
	if si := encode(cfgSkip); si.MD5 != zero {
		t.Errorf("FrameEncoder SkipMD5 MD5 = %x, want zero", si.MD5)
	}
	if si, want := encode(cfg), md5.Sum(pcm); si.MD5 != want {
		t.Errorf("FrameEncoder default MD5 = %x, want md5(input) = %x", si.MD5, want)
	}
}

// TestDecoderSkipMD5VerificationBypassesMismatch pins that SkipMD5Verification ignores a
// corrupted STREAMINFO MD5 and decodes cleanly, with audio intact. Sabotage: dropping
// `!d.skipMD5 &&` from finish makes the corrupted MD5 verify and reddens this.
func TestDecoderSkipMD5VerificationBypassesMismatch(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	good := makeStream(t, cfg, 9000)
	bad := corruptMD5(good)

	// Sanity: the default decode of the corrupted stream fails verification, so the
	// positive case below actually exercises the skip path.
	if _, err := io.Copy(io.Discard, mustDecoder(t, bytes.NewReader(bad))); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("default decode of corrupted stream: err = %v, want ErrMD5Mismatch", err)
	}

	d, err := NewDecoder(bytes.NewReader(bad), SkipMD5Verification())
	if err != nil {
		t.Fatalf("NewDecoder(SkipMD5Verification): %v", err)
	}
	var got bytes.Buffer
	if _, err := io.Copy(&got, d); err != nil {
		t.Fatalf("decode with SkipMD5Verification: unexpected err = %v", err)
	}
	// The MD5 field does not affect the audio, so the decode matches the good stream.
	_, want := decodeAll(t, bytes.NewReader(good))
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("SkipMD5Verification decode differs from good decode: %d vs %d bytes", got.Len(), len(want))
	}
}

// TestDecoderResetSkipMD5Verification pins that Reset accepts the option (pooling
// ergonomics) and that the policy is re-derived per call, not remembered: a Reset
// without the option verifies again. Sabotage: dropping the options loop in Reset, or
// the `d.skipMD5 = false` reset in init, reddens one of the two halves.
func TestDecoderResetSkipMD5Verification(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	bad := corruptMD5(makeStream(t, cfg, 9000))

	var d Decoder
	if err := d.Reset(bytes.NewReader(bad), SkipMD5Verification()); err != nil {
		t.Fatalf("Reset(SkipMD5Verification): %v", err)
	}
	if _, err := io.Copy(io.Discard, &d); err != nil {
		t.Fatalf("decode after Reset(SkipMD5Verification): unexpected err = %v", err)
	}

	// A plain Reset must restore the default (verify), so the same corrupted stream now
	// fails. This pins that the skip policy does not leak across Reset.
	if err := d.Reset(bytes.NewReader(bad)); err != nil {
		t.Fatalf("Reset(default): %v", err)
	}
	if _, err := io.Copy(io.Discard, &d); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("decode after Reset(default): err = %v, want ErrMD5Mismatch (skip policy must not persist)", err)
	}
}

// TestDecoderSkipMD5VerificationRetainsTruncationCheck pins that skipping MD5
// verification still catches an inter-frame truncation via the sample-count check, even
// on a stream that carries a real (non-zero) MD5: the default path returns
// ErrMD5Mismatch (the short decode does not match the stored MD5), while the skip path
// falls through to ErrTruncatedStream rather than a silent clean EOF. Sabotage: dropping
// `!d.skipMD5 &&` from finish makes the skip path return ErrMD5Mismatch too.
func TestDecoderSkipMD5VerificationRetainsTruncationCheck(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 3*4096) // real MD5, total_samples declared
	cut := truncateAtFrameBoundary(t, data, 2)

	if _, err := io.Copy(io.Discard, mustDecoder(t, bytes.NewReader(cut))); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("default decode of truncated stream: err = %v, want ErrMD5Mismatch", err)
	}

	d, err := NewDecoder(bytes.NewReader(cut), SkipMD5Verification())
	if err != nil {
		t.Fatalf("NewDecoder(SkipMD5Verification): %v", err)
	}
	if _, err := io.Copy(io.Discard, d); !errors.Is(err, flac.ErrTruncatedStream) {
		t.Fatalf("truncated decode with SkipMD5Verification: err = %v, want ErrTruncatedStream", err)
	}
}

// TestEncodeInterleavedSkipMD5Seekable pins the one-shot path against a SEEKABLE sink,
// where EncodeInterleaved's Close rewrites the STREAMINFO placeholder: that rewrite must
// leave the MD5 all-zero under SkipMD5 rather than patch a digest. Sabotage: dropping the
// `if !e.skipHash` guard in Close reddens the zero-MD5 assertion.
func TestEncodeInterleavedSkipMD5Seekable(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5, SkipMD5: true}
	nSamples := 4096 + 300
	pcm := genPCM(cfg, nSamples)

	var sink seekBuffer
	if err := EncodeInterleaved(&sink, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved(seekable, SkipMD5): %v", err)
	}
	si, samples := decodeAll(t, bytes.NewReader(sink.Bytes()))
	var zero [16]byte
	if si.MD5 != zero {
		t.Errorf("seekable EncodeInterleaved SkipMD5 MD5 = %x, want zero", si.MD5)
	}
	if si.TotalSamples != uint64(nSamples) {
		t.Errorf("seekable EncodeInterleaved SkipMD5 total_samples = %d, want %d", si.TotalSamples, nSamples)
	}
	if !bytes.Equal(samples, pcm) {
		t.Error("seekable EncodeInterleaved SkipMD5 round-trip: decoded PCM differs from input")
	}
}

// TestFrameEncoderIncrementalSkipMD5 pins the FrameEncoder incremental Write/Finalize
// path (distinct from its one-shot EncodeInterleaved) under SkipMD5: the finalized
// STREAMINFO MD5 must be zero. Sabotage: dropping the `if !e.skipHash` guard in
// streamInfoParams reddens the zero-MD5 assertion.
func TestFrameEncoderIncrementalSkipMD5(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5, SkipMD5: true}
	pcm := genPCM(cfg, 4096+256)

	fe, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}
	noop := func([]byte, int) error { return nil }
	// Feed several chunks so Write emits whole frames and Finalize handles the tail.
	for off := 0; off < len(pcm); off += 777 {
		end := min(off+777, len(pcm))
		if _, err := fe.Write(pcm[off:end], noop); err != nil {
			t.Fatalf("FrameEncoder.Write: %v", err)
		}
	}
	if err := fe.Finalize(noop); err != nil {
		t.Fatalf("FrameEncoder.Finalize: %v", err)
	}
	var zero [16]byte
	if si := fe.StreamInfo(); si.MD5 != zero {
		t.Errorf("FrameEncoder incremental SkipMD5 MD5 = %x, want zero", si.MD5)
	}
}

// TestDecodeInterleavedSkipMD5Verification pins that SkipMD5Verification forwards through
// the one-shot DecodeInterleaved wrapper into NewDecoder: a corrupted-MD5 stream decodes
// clean under the option and fails without it. Sabotage: dropping the opts forwarding in
// DecodeInterleaved (or DecodeInterleavedLimit) reddens the clean-decode half.
func TestDecodeInterleavedSkipMD5Verification(t *testing.T) {
	cfg := Config{SampleRate: 44100, BitDepth: 16, Channels: 2, CompressionLevel: 5}
	good := makeStream(t, cfg, 6000)
	bad := corruptMD5(good)

	if _, _, err := DecodeInterleaved(bytes.NewReader(bad)); !errors.Is(err, flac.ErrMD5Mismatch) {
		t.Fatalf("DecodeInterleaved(default) of corrupted stream: err = %v, want ErrMD5Mismatch", err)
	}
	got, _, err := DecodeInterleaved(bytes.NewReader(bad), SkipMD5Verification())
	if err != nil {
		t.Fatalf("DecodeInterleaved(SkipMD5Verification): unexpected err = %v", err)
	}
	_, want := decodeAll(t, bytes.NewReader(good))
	if !bytes.Equal(got, want) {
		t.Fatalf("DecodeInterleaved SkipMD5Verification decode differs from good decode: %d vs %d bytes", len(got), len(want))
	}
}

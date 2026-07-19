package pcm

import (
	"bytes"
	"crypto/md5"
	"errors"
	"testing"

	"github.com/tphakala/go-flac/internal/meta"
)

// collectFrames runs a FrameEncoder over pcm and returns each frame (copied) with
// its block size.
func collectFrames(t *testing.T, cfg Config, pcm []byte) (frames [][]byte, blockSizes []int, e *FrameEncoder) {
	t.Helper()
	e, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}
	err = e.EncodeInterleaved(pcm, func(fr []byte, bs int) error {
		frames = append(frames, bytes.Clone(fr))
		blockSizes = append(blockSizes, bs)
		return nil
	})
	if err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}
	return frames, blockSizes, e
}

func TestFrameEncoderDecoderRoundTrip(t *testing.T) {
	cases := []struct {
		name         string
		sampleRate   int
		channels     int
		bitDepth     int
		samplesPerCh int
	}{
		{"mono16_44100_short", 44100, 1, 16, 100},           // single short frame
		{"mono16_48000_exactblock", 48000, 1, 16, 4096},     // one full block, no short frame
		{"stereo16_48000_multi", 48000, 2, 16, 4096*3 + 77}, // multiple blocks + short final
		{"mono24_48000", 48000, 1, 24, 9000},
		{"stereo24_44100", 44100, 2, 24, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SampleRate: tc.sampleRate, Channels: tc.channels, BitDepth: tc.bitDepth, CompressionLevel: 5}
			pcm := genPCM(cfg, tc.samplesPerCh)

			frames, blockSizes, enc := collectFrames(t, cfg, pcm)
			if len(frames) == 0 {
				t.Fatal("no frames emitted")
			}

			// Block sizes: every frame but the last is a full block; the total matches.
			total := 0
			for i, bs := range blockSizes {
				total += bs
				if i < len(blockSizes)-1 && bs != encoderBlockSize {
					t.Errorf("frame %d block size %d, want %d (only the last may be short)", i, bs, encoderBlockSize)
				}
			}
			if total != tc.samplesPerCh {
				t.Errorf("block sizes sum to %d, want %d samples", total, tc.samplesPerCh)
			}

			// Decode every frame with a decoder built from the encoder's STREAMINFO and
			// reassemble the PCM; it must be byte-identical to the input.
			dec, err := NewFrameDecoder(enc.StreamInfoBytes())
			if err != nil {
				t.Fatalf("NewFrameDecoder: %v", err)
			}
			var got []byte
			for i, fr := range frames {
				out, bs, err := dec.DecodeInterleaved(fr)
				if err != nil {
					t.Fatalf("DecodeInterleaved frame %d: %v", i, err)
				}
				if bs != blockSizes[i] {
					t.Errorf("frame %d decoded block size %d, want %d", i, bs, blockSizes[i])
				}
				got = append(got, out...)
			}
			if !bytes.Equal(got, pcm) {
				t.Errorf("round-trip PCM mismatch: got %d bytes, want %d bytes", len(got), len(pcm))
			}
		})
	}
}

// TestFrameEncoderMatchesStreamEncoder asserts the frames a FrameEncoder emits are
// byte-identical to the audio-frame region of the stream Encoder writes for the
// same input and Config, so the shim adds no divergence from the streaming path.
func TestFrameEncoderMatchesStreamEncoder(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	pcm := genPCM(cfg, 4096*2+123)

	frames, _, _ := collectFrames(t, cfg, pcm)
	var joined []byte
	for _, fr := range frames {
		joined = append(joined, fr...)
	}

	var stream bytes.Buffer
	if err := EncodeInterleaved(&stream, cfg, pcm); err != nil {
		t.Fatalf("EncodeInterleaved (stream): %v", err)
	}
	audio := audioRegion(t, stream.Bytes())

	if !bytes.Equal(joined, audio) {
		t.Errorf("frame bytes differ from the stream's audio region: %d vs %d bytes", len(joined), len(audio))
	}
}

// audioRegion returns the concatenated audio frames of a native FLAC stream by
// skipping the "fLaC" marker and every metadata block.
func audioRegion(t *testing.T, stream []byte) []byte {
	t.Helper()
	if len(stream) < 4 || string(stream[:4]) != "fLaC" {
		t.Fatalf("stream does not start with fLaC marker")
	}
	off := 4
	for {
		if off+4 > len(stream) {
			t.Fatalf("metadata blocks overrun stream")
		}
		last := stream[off]&0x80 != 0
		length := int(stream[off+1])<<16 | int(stream[off+2])<<8 | int(stream[off+3])
		off += 4 + length
		if last {
			break
		}
	}
	return stream[off:]
}

func TestFrameEncoderStreamInfo(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	pcm := genPCM(cfg, 10000)

	enc, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}

	// Before encoding: valid 34-byte body with a non-zero (fixed) block size.
	pre := enc.StreamInfoBytes()
	if len(pre) != meta.StreamInfoBodyLen {
		t.Fatalf("StreamInfoBytes = %d bytes, want %d", len(pre), meta.StreamInfoBodyLen)
	}
	minBlockPre := int(pre[0])<<8 | int(pre[1])
	if minBlockPre == 0 {
		t.Error("up-front STREAMINFO advertises block size 0; strict decoders derive every frame's sample number as 0")
	}
	if siPre, err := meta.DecodeStreamInfo(pre); err != nil {
		t.Fatalf("DecodeStreamInfo(pre): %v", err)
	} else if siPre.SampleRate != cfg.SampleRate || siPre.Channels != cfg.Channels || siPre.BitDepth != cfg.BitDepth {
		t.Errorf("pre STREAMINFO = %+v, want rate/ch/bps %d/%d/%d", siPre, cfg.SampleRate, cfg.Channels, cfg.BitDepth)
	}

	if err := enc.EncodeInterleaved(pcm, func([]byte, int) error { return nil }); err != nil {
		t.Fatalf("EncodeInterleaved: %v", err)
	}

	post := enc.StreamInfoBytes()
	si, err := meta.DecodeStreamInfo(post)
	if err != nil {
		t.Fatalf("DecodeStreamInfo(post): %v", err)
	}
	if si.TotalSamples != 10000 {
		t.Errorf("post STREAMINFO TotalSamples = %d, want 10000", si.TotalSamples)
	}
	var zero [16]byte
	if si.MD5 == zero {
		t.Error("post STREAMINFO MD5 is zero; expected the measured input digest")
	}
	// min/max frame sizes (bytes 4..6 and 7..9) are measured after encoding.
	minFrame := int(post[4])<<16 | int(post[5])<<8 | int(post[6])
	maxFrame := int(post[7])<<16 | int(post[8])<<8 | int(post[9])
	if minFrame == 0 || maxFrame == 0 || minFrame > maxFrame {
		t.Errorf("post STREAMINFO frame sizes min=%d max=%d, want non-zero and min<=max", minFrame, maxFrame)
	}

	// The MD5 must match the standard library digest of the raw input.
	want := md5Sum(pcm)
	if si.MD5 != want {
		t.Errorf("STREAMINFO MD5 = %x, want %x", si.MD5, want)
	}
}

func md5Sum(b []byte) [16]byte {
	return md5.Sum(b)
}

// TestFrameEncoderEmptyInput checks that encoding zero samples still finalizes a
// spec-legal STREAMINFO: the MD5 of no bytes, zero total samples, and a floored
// (non-zero) block size, matching what the streaming Encoder writes for an empty
// stream.
func TestFrameEncoderEmptyInput(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}
	enc, err := NewFrameEncoder(cfg)
	if err != nil {
		t.Fatalf("NewFrameEncoder: %v", err)
	}
	n := 0
	if err := enc.EncodeInterleaved(nil, func([]byte, int) error { n++; return nil }); err != nil {
		t.Fatalf("EncodeInterleaved(nil): %v", err)
	}
	if n != 0 {
		t.Errorf("emit called %d times for empty input, want 0", n)
	}
	body := enc.StreamInfoBytes()
	si, err := meta.DecodeStreamInfo(body)
	if err != nil {
		t.Fatalf("DecodeStreamInfo: %v", err)
	}
	if si.TotalSamples != 0 {
		t.Errorf("TotalSamples = %d, want 0", si.TotalSamples)
	}
	if got, want := si.MD5, md5Sum(nil); got != want {
		t.Errorf("MD5 = %x, want the empty-input digest %x", got, want)
	}
	if minBlock := int(body[0])<<8 | int(body[1]); minBlock == 0 {
		t.Error("empty-stream STREAMINFO advertises block size 0")
	}
}

func TestFrameEncoderErrors(t *testing.T) {
	cfg := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}

	t.Run("bad config", func(t *testing.T) {
		if _, err := NewFrameEncoder(Config{SampleRate: 0, Channels: 1, BitDepth: 16}); err == nil {
			t.Error("expected error for zero sample rate")
		}
	})

	t.Run("partial trailing sample", func(t *testing.T) {
		enc, _ := NewFrameEncoder(cfg)
		// frameLen = 2 bytes * 2 channels = 4; give 6 bytes (not a whole sample).
		err := enc.EncodeInterleaved([]byte{0, 0, 0, 0, 0, 0}, func([]byte, int) error { return nil })
		if err == nil {
			t.Error("expected error for a partial trailing interleaved sample")
		}
	})

	t.Run("second call", func(t *testing.T) {
		enc, _ := NewFrameEncoder(cfg)
		pcm := genPCM(cfg, 4096)
		if err := enc.EncodeInterleaved(pcm, func([]byte, int) error { return nil }); err != nil {
			t.Fatalf("first EncodeInterleaved: %v", err)
		}
		if err := enc.EncodeInterleaved(pcm, func([]byte, int) error { return nil }); err == nil {
			t.Error("expected error on a second EncodeInterleaved call")
		}
	})

	t.Run("total samples mismatch", func(t *testing.T) {
		c := cfg
		c.TotalSamples = 5000
		enc, _ := NewFrameEncoder(c)
		pcm := genPCM(c, 4096) // 4096 != declared 5000
		if err := enc.EncodeInterleaved(pcm, func([]byte, int) error { return nil }); err == nil {
			t.Error("expected error when encoded count disagrees with Config.TotalSamples")
		}
	})

	t.Run("emit error propagates", func(t *testing.T) {
		enc, _ := NewFrameEncoder(cfg)
		pcm := genPCM(cfg, 4096)
		sentinel := errTest
		err := enc.EncodeInterleaved(pcm, func([]byte, int) error { return sentinel })
		if !errors.Is(err, sentinel) {
			t.Errorf("EncodeInterleaved error = %v, want the emit sentinel", err)
		}
	})
}

func TestFrameDecoderErrors(t *testing.T) {
	t.Run("short streaminfo", func(t *testing.T) {
		if _, err := NewFrameDecoder([]byte{1, 2, 3}); err == nil {
			t.Error("expected error for a STREAMINFO body that is not 34 bytes")
		}
	})

	t.Run("channel mismatch", func(t *testing.T) {
		// A mono frame decoded against a STREAMINFO that declares stereo must be
		// rejected rather than silently producing wrong interleaving.
		monoCfg := Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}
		frames, _, _ := collectFrames(t, monoCfg, genPCM(monoCfg, 4096))

		stereoCfg := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}
		stereoEnc, _ := NewFrameEncoder(stereoCfg)
		dec, err := NewFrameDecoder(stereoEnc.StreamInfoBytes())
		if err != nil {
			t.Fatalf("NewFrameDecoder: %v", err)
		}
		if _, _, err := dec.DecodeInterleaved(frames[0]); err == nil {
			t.Error("expected a channel-count mismatch error decoding a mono frame as stereo")
		}
	})
}

// errTest is a package-local sentinel for the emit-error propagation test.
var errTest = &testError{"test emit failure"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }

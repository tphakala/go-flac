package pcm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/meta"
)

const (
	pathReadAll = "ReadAll"
	pathWriteTo = "WriteTo"
	pathOneShot = "DecodeInterleavedLimit"
)

func geometry(rate, channels, bitDepth int) string {
	return fmt.Sprintf("(%d Hz, %d ch, %d bps)", rate, channels, bitDepth)
}

// decodeByPath decodes stream through one of the three whole-stream paths and
// returns the bytes delivered before the error.
func decodeByPath(t *testing.T, path string, stream []byte, limit int, opts []DecoderOption) (got []byte, err error) {
	t.Helper()
	if path == pathOneShot {
		got, _, err = DecodeInterleavedLimit(bytes.NewReader(stream), limit, opts...)
		return got, err
	}
	dec, nerr := NewDecoder(bytes.NewReader(stream), opts...)
	if nerr != nil {
		t.Fatalf("NewDecoder: %v", nerr)
	}
	if path == pathReadAll {
		return io.ReadAll(dec)
	}
	var buf bytes.Buffer
	_, err = dec.WriteTo(&buf)
	return buf.Bytes(), err
}

// TestDecoderRejectsStreamInfoMismatch appends frames of another layout to a
// valid stream. The frames are CRC-valid, so only the STREAMINFO comparison can
// reject them.
func TestDecoderRejectsStreamInfoMismatch(t *testing.T) {
	cfgA := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	aPCM := genPCM(cfgA, 48000)
	a := encodeFLAC(t, cfgA, aPCM)

	tails := []struct {
		name string
		cfg  Config
		n    int
	}{
		{"mono odd final block", Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}, 4097},
		{"mono even blocks", Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}, 8192},
		{"other rate", Config{SampleRate: 44100, Channels: 2, BitDepth: 16, CompressionLevel: 5}, 5000},
		{"other depth", Config{SampleRate: 48000, Channels: 2, BitDepth: 24, CompressionLevel: 5}, 5000},
	}
	optSets := []struct {
		name string
		opts []DecoderOption
		want []byte
	}{
		{"md5", nil, aPCM},
		{"skip md5", []DecoderOption{SkipMD5Verification()}, aPCM},
		{"skip md5 convert", []DecoderOption{SkipMD5Verification(), WithConvertTo(24)}, decodeOpts(t, a, WithConvertTo(24))},
	}
	for _, tail := range tails {
		frames, _, _ := collectFrames(t, tail.cfg, genPCM(tail.cfg, tail.n))
		stream := bytes.Clone(a)
		for _, f := range frames {
			stream = append(stream, f...)
		}
		for _, oset := range optSets {
			for _, path := range []string{pathReadAll, pathWriteTo, pathOneShot} {
				t.Run(tail.name+"/"+oset.name+"/"+path, func(t *testing.T) {
					got, err := decodeByPath(t, path, stream, len(oset.want), oset.opts)
					if !errors.Is(err, flac.ErrStreamInfoMismatch) {
						t.Fatalf("error = %v, want flac.ErrStreamInfoMismatch", err)
					}
					if errors.Is(err, ErrDecodeLimit) {
						t.Fatalf("error %v is also reported as ErrDecodeLimit", err)
					}
					for _, want := range []string{geometry(48000, 2, 16), geometry(tail.cfg.SampleRate, tail.cfg.Channels, tail.cfg.BitDepth)} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("error %q lacks %s", err, want)
						}
					}
					if path == pathOneShot {
						if got != nil {
							t.Fatalf("one-shot returned %d bytes with an error, want nil", len(got))
						}
						return
					}
					if !bytes.Equal(got, oset.want) {
						t.Fatalf("delivered %d bytes before the error, want the %d-byte valid prefix", len(got), len(oset.want))
					}
				})
			}
		}
	}
}

// TestDecoderStreamInfoMismatchMidStream splices one mono frame into an
// otherwise valid stereo stream, with good frames after it and unbroken frame
// numbers, so a decoder that stops caring after the bad frame is caught.
func TestDecoderStreamInfoMismatchMidStream(t *testing.T) {
	const blk, nFrames = 4096, 8
	cfgA := Config{SampleRate: 48000, Channels: 2, BitDepth: 16, CompressionLevel: 5}
	cfgB := Config{SampleRate: 48000, Channels: 1, BitDepth: 16, CompressionLevel: 5}
	aPCM := genPCM(cfgA, nFrames*blk)
	aFrames, _, encA := collectFrames(t, cfgA, aPCM)
	bFrames, _, _ := collectFrames(t, cfgB, genPCM(cfgB, nFrames*blk))
	if len(aFrames) != nFrames || len(bFrames) != nFrames {
		t.Fatalf("frames: A %d, B %d, want %d", len(aFrames), len(bFrames), nFrames)
	}
	const frameBytes = 4 // 2 channels * 2 bytes

	build := func(bad int) []byte {
		var s bytes.Buffer
		if err := meta.WriteStreamHeader(&s, encA.StreamInfoBytes()); err != nil {
			t.Fatal(err)
		}
		for i, f := range aFrames {
			if i == bad {
				f = bFrames[i]
			}
			s.Write(f)
		}
		return s.Bytes()
	}
	newDec := func(t *testing.T, stream []byte) *Decoder {
		t.Helper()
		d, err := NewDecoder(bytes.NewReader(stream))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	readAll := func(t *testing.T, d *Decoder, want []byte, wantErr bool) {
		t.Helper()
		got, err := io.ReadAll(d)
		if wantErr && !errors.Is(err, flac.ErrStreamInfoMismatch) || !wantErr && err != nil {
			t.Fatalf("ReadAll error = %v, want mismatch=%v", err, wantErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadAll delivered %d bytes, want %d", len(got), len(want))
		}
	}
	seek := func(t *testing.T, d *Decoder, target int64, wantOK bool) {
		t.Helper()
		pos, err := d.SeekToSample(target)
		if wantOK && (err != nil || pos != target) {
			t.Fatalf("SeekToSample(%d) = (%d, %v), want (%d, nil)", target, pos, err, target)
		}
		if !wantOK && (pos != 0 || !errors.Is(err, flac.ErrStreamInfoMismatch)) {
			t.Fatalf("SeekToSample(%d) = (%d, %v), want (0, ErrStreamInfoMismatch)", target, pos, err)
		}
	}

	t.Run("middle frame", func(t *testing.T) {
		const k = 4
		d := newDec(t, build(k))
		readAll(t, d, aPCM[:k*blk*frameBytes], true)
		// Frame 5 is valid, so only a sticky error stops Read here.
		if n, err := d.Read(make([]byte, 64)); n != 0 || !errors.Is(err, flac.ErrStreamInfoMismatch) {
			t.Fatalf("Read after the error = (%d, %v), want (0, ErrStreamInfoMismatch)", n, err)
		}
		seek(t, d, 2*blk+5, true)
		readAll(t, d, aPCM[(2*blk+5)*frameBytes:k*blk*frameBytes], true)
		seek(t, d, k*blk+10, false)
		readAll(t, d, nil, true)
		seek(t, d, 5*blk+7, true)
		readAll(t, d, aPCM[(5*blk+7)*frameBytes:], false)
		seek(t, d, 0, true)
		readAll(t, d, aPCM[:k*blk*frameBytes], true)
		if err := d.Reset(bytes.NewReader(build(-1))); err != nil {
			t.Fatal(err)
		}
		readAll(t, d, aPCM, false)
	})

	t.Run("first frame", func(t *testing.T) {
		stream := build(0)
		readAll(t, newDec(t, stream), nil, true)
		seek(t, newDec(t, stream), 10, false)
	})
}

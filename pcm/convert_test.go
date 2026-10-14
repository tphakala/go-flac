package pcm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	flac "github.com/tphakala/go-flac"
)

// genInRange returns n inter-channel samples of interleaved values that are in
// range for a signed bits-wide sample: the extremes, 0 and -1 explicitly, then a
// deterministic full-scale LCG fill masked and sign-extended to bits.
func genInRange(bits, channels, n int) []int32 {
	out := make([]int32, n*channels)
	sext := func(u uint32) int32 {
		s := uint(32 - bits)
		return int32(u<<s) >> s
	}
	fixed := []int32{
		int32(-(int64(1) << (bits - 1))),
		int32((int64(1) << (bits - 1)) - 1),
		0,
		-1,
	}
	seed := uint32(bits*7919 + channels*104729)
	for i := range out {
		if i < len(fixed) {
			out[i] = fixed[i]
			continue
		}
		seed = seed*1664525 + 1013904223
		out[i] = sext(seed >> 3)
	}
	return out
}

// unpackSamples reads little-endian signed samples of bytesPS bytes, sign-extending
// from the top of the container.
func unpackLE(b []byte, bytesPS int) []int32 {
	out := make([]int32, 0, len(b)/bytesPS)
	for i := 0; i+bytesPS <= len(b); i += bytesPS {
		var u uint32
		for k := range bytesPS {
			u |= uint32(b[i+k]) << (uint(k) * 8)
		}
		s := uint(32 - 8*bytesPS)
		out = append(out, int32(u<<s)>>s)
	}
	return out
}

// refConvert is the shift-free oracle: widening multiplies, narrowing floors.
func refConvert(v int32, src, dst int) int32 {
	if dst >= src {
		return int32(int64(v) * (int64(1) << uint(dst-src)))
	}
	d := int64(1) << uint(src-dst)
	q := int64(v) / d
	if int64(v)%d != 0 && v < 0 {
		q-- // Go truncates toward zero; floor for negative remainders
	}
	return int32(q)
}

func convStream(t *testing.T, bits, channels, n int) (stream []byte, vals []int32) {
	t.Helper()
	vals = genInRange(bits, channels, n)
	cfg := Config{SampleRate: 44100, BitDepth: bits, Channels: channels, CompressionLevel: 5}
	return encodeFLAC(t, cfg, packLE(vals, bits)), vals
}

func decodeOpts(t *testing.T, stream []byte, opts ...DecoderOption) []byte {
	t.Helper()
	d, err := NewDecoder(bytes.NewReader(stream), opts...)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return buf.Bytes()
}

func readOddChunks(t *testing.T, d *Decoder, chunk int) []byte {
	t.Helper()
	var out []byte
	p := make([]byte, chunk)
	for {
		n, err := d.Read(p)
		out = append(out, p[:n]...)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
}

func TestWithConvertToMatchesReference(t *testing.T) {
	for src := 4; src <= 32; src++ {
		for _, dst := range []int{8, 16, 24, 32} {
			for _, ch := range []int{1, 2, 3} {
				t.Run(fmt.Sprintf("%dto%d/%dch", src, dst, ch), func(t *testing.T) {
					checkConvertAgainstReference(t, src, dst, ch)
				})
			}
		}
	}
}

func checkConvertAgainstReference(t *testing.T, src, dst, ch int) {
	t.Helper()
	stream, vals := convStream(t, src, ch, 6000)
	native := unpackLE(decodeOpts(t, stream), (src+7)/8)
	if len(native) != len(vals) {
		t.Fatalf("native decode has %d samples, want %d", len(native), len(vals))
	}

	d, err := NewDecoder(bytes.NewReader(stream), WithConvertTo(dst))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := d.OutputBitDepth(); got != dst {
		t.Errorf("OutputBitDepth() = %d, want %d", got, dst)
	}
	ref, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if d.Info() != ref.Info() {
		t.Errorf("Info() = %+v, want native %+v", d.Info(), ref.Info())
	}
	chunked := readOddChunks(t, d, 7) // EOF with nil error also proves MD5 passed

	d2, err := NewDecoder(bytes.NewReader(stream), WithConvertTo(dst))
	if err != nil {
		t.Fatal(err)
	}
	var wt bytes.Buffer
	if _, err := d2.WriteTo(&wt); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if !bytes.Equal(chunked, wt.Bytes()) {
		t.Fatal("chunked Read and WriteTo disagree")
	}
	if want := len(native) * dst / 8; len(chunked) != want {
		t.Fatalf("output length %d, want %d", len(chunked), want)
	}
	got := unpackLE(chunked, dst/8)
	for i, v := range native {
		if w := refConvert(v, src, dst); got[i] != w {
			t.Fatalf("sample %d: native %d -> %d, want %d", i, v, got[i], w)
		}
	}
}

func TestWithConvertToIdentityIsByteIdentical(t *testing.T) {
	for _, bits := range []int{8, 16, 24, 32} {
		stream, _ := convStream(t, bits, 2, 6000)
		want := decodeOpts(t, stream)
		got := decodeOpts(t, stream, WithConvertTo(bits))
		if !bytes.Equal(got, want) {
			t.Errorf("WithConvertTo(%d) on a %d-bit stream differs from the default decode", bits, bits)
		}
	}
}

func TestWithConvertToMD5VerifiedOnNativeSamples(t *testing.T) {
	pairs := [][2]int{{12, 16}, {24, 16}, {16, 24}, {20, 32}, {32, 8}}
	for _, p := range pairs {
		src, dst := p[0], p[1]
		t.Run(fmt.Sprintf("%dto%d", src, dst), func(t *testing.T) {
			stream, _ := convStream(t, src, 2, 6000)
			good := decodeOpts(t, stream, WithConvertTo(dst)) // fatals on any error

			bad := corruptMD5(stream)
			d, err := NewDecoder(bytes.NewReader(bad), WithConvertTo(dst))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, d); !errors.Is(err, flac.ErrMD5Mismatch) {
				t.Errorf("corrupt MD5 with conversion: err = %v, want ErrMD5Mismatch", err)
			}

			skipped := decodeOpts(t, bad, WithConvertTo(dst), SkipMD5Verification())
			if !bytes.Equal(skipped, good) {
				t.Error("skip-MD5 converted output differs from the good stream's converted output")
			}
		})
	}
}

func TestWithConvertToSeek(t *testing.T) {
	for _, src := range []int{8, 12, 16, 20, 24, 32} {
		for _, dst := range []int{8, 16, 32} {
			t.Run(fmt.Sprintf("%dto%d", src, dst), func(t *testing.T) {
				checkConvertSeek(t, src, dst)
			})
		}
	}
}

func checkConvertSeek(t *testing.T, src, dst int) {
	t.Helper()
	const block = encoderBlockSize
	const ch = 2
	total := 3*block + 123
	stream, _ := convStream(t, src, ch, total)
	native := unpackLE(decodeOpts(t, stream), (src+7)/8)
	conv := func(from int) []byte {
		return packLE(applyRef(native[from*ch:], src, dst), dst)
	}
	open := func() *Decoder {
		d, err := NewDecoder(bytes.NewReader(stream), WithConvertTo(dst))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, tg := range []int{0, 1, block - 1, block, block + 1, block + block/2 + 3, total - 1} {
		d := open()
		got, err := d.SeekToSample(int64(tg))
		if err != nil || got != int64(tg) {
			t.Fatalf("SeekToSample(%d) = %d, %v", tg, got, err)
		}
		out, err := io.ReadAll(d)
		if err != nil {
			t.Fatalf("read after seek %d: %v", tg, err)
		}
		if !bytes.Equal(out, conv(tg)) {
			t.Fatalf("output after seek to %d differs from converted native tail", tg)
		}
	}
	for _, tg := range []int{total, total + 5} {
		d := open()
		if _, err := d.SeekToSample(int64(tg)); err != nil {
			t.Fatalf("SeekToSample(%d): %v", tg, err)
		}
		if n, err := d.Read(make([]byte, 16)); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read past end after seek %d = %d, %v; want 0, EOF", tg, n, err)
		}
	}

	// Seek, read part way, seek backwards, read again.
	d := open()
	if _, err := d.SeekToSample(block + 10); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(d, make([]byte, 100*ch*dst/8)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SeekToSample(5); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, conv(5)) {
		t.Fatal("output after backward seek differs from converted native tail")
	}
}

func applyRef(native []int32, src, dst int) []int32 {
	out := make([]int32, len(native))
	for i, v := range native {
		out[i] = refConvert(v, src, dst)
	}
	return out
}

func TestWithConvertToInvalid(t *testing.T) {
	stream, _ := convStream(t, 16, 2, 6000)
	for _, v := range []int{0, -1, 4, 12, 20, 31, 33, 64} {
		d, err := NewDecoder(bytes.NewReader(stream), WithConvertTo(v))
		if d != nil {
			t.Errorf("WithConvertTo(%d): decoder = %v, want nil", v, d)
		}
		if !errors.Is(err, flac.ErrUnsupported) {
			t.Errorf("WithConvertTo(%d): err = %v, want ErrUnsupported", v, err)
		} else if !bytes.Contains([]byte(err.Error()), []byte("WithConvertTo")) {
			t.Errorf("WithConvertTo(%d): error %q does not name WithConvertTo", v, err)
		}
	}

	// A decoder that already decoded a tagged, seekable stream, then fails a Reset.
	var tagged bytes.Buffer
	enc, err := NewEncoder(&tagged, Config{SampleRate: 44100, BitDepth: 16, Channels: 2,
		Tags: []flac.Tag{{Name: "TITLE", Value: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(genPCM(Config{BitDepth: 16, Channels: 2}, 6000)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := NewDecoder(bytes.NewReader(tagged.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Tags()) == 0 {
		t.Fatal("test stream has no tags")
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		t.Fatal(err)
	}
	rerr := d.Reset(bytes.NewReader(tagged.Bytes()), WithConvertTo(12))
	if !errors.Is(rerr, flac.ErrUnsupported) {
		t.Fatalf("Reset err = %v, want ErrUnsupported", rerr)
	}
	if d.Info() != (flac.StreamInfo{}) {
		t.Errorf("Info() = %+v after failed Reset, want zero", d.Info())
	}
	if d.Tags() != nil || d.Vendor() != "" {
		t.Errorf("tags/vendor leaked after failed Reset: %v %q", d.Tags(), d.Vendor())
	}
	if d.OutputBitDepth() != 0 {
		t.Errorf("OutputBitDepth() = %d after failed Reset, want 0", d.OutputBitDepth())
	}
	if _, err := d.Read(make([]byte, 8)); !errors.Is(err, flac.ErrUnsupported) {
		t.Errorf("Read after failed Reset = %v, want the Reset error", err)
	}
	if _, err := d.SeekToSample(0); err == nil {
		t.Error("SeekToSample(0) on a poisoned decoder succeeded; it must not revive it")
	}
	if _, err := d.Read(make([]byte, 8)); err == nil {
		t.Error("decoder revived by a seek")
	}
	if err := d.Reset(bytes.NewReader(tagged.Bytes())); err != nil {
		t.Fatalf("valid Reset after failure: %v", err)
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		t.Errorf("decode after recovery: %v", err)
	}

	if _, _, err := DecodeInterleaved(bytes.NewReader(stream), WithConvertTo(20)); !errors.Is(err, flac.ErrUnsupported) {
		t.Errorf("DecodeInterleaved(WithConvertTo(20)) err = %v, want ErrUnsupported", err)
	}
}

type convStep struct {
	bits int
	to   int // 0 means no option
}

func (s convStep) opts() []DecoderOption {
	if s.to == 0 {
		return nil
	}
	return []DecoderOption{WithConvertTo(s.to)}
}

func TestDecoderResetConvertToNotRemembered(t *testing.T) {
	seq := []convStep{{24, 16}, {24, 0}, {12, 32}, {32, 8}, {12, 0}}
	streams := map[int][]byte{}
	for _, s := range seq {
		if _, ok := streams[s.bits]; !ok {
			streams[s.bits], _ = convStream(t, s.bits, 2, 9000)
		}
	}
	for _, reversed := range []bool{false, true} {
		for _, seekable := range []bool{true, false} {
			t.Run(fmt.Sprintf("reversed=%v/seekable=%v", reversed, seekable), func(t *testing.T) {
				order := slices.Clone(seq)
				if reversed {
					slices.Reverse(order)
				}
				d := new(Decoder)
				for i, s := range order {
					checkResetStep(t, d, i, s, streams[s.bits], seekable)
				}
			})
		}
	}
}

func checkResetStep(t *testing.T, d *Decoder, i int, s convStep, stream []byte, seekable bool) {
	t.Helper()
	if err := d.Reset(sourceFor(stream, seekable), s.opts()...); err != nil {
		t.Fatalf("step %d Reset: %v", i, err)
	}
	fresh, err := NewDecoder(sourceFor(stream, seekable), s.opts()...)
	if err != nil {
		t.Fatal(err)
	}
	if d.OutputBitDepth() != fresh.OutputBitDepth() {
		t.Fatalf("step %d: OutputBitDepth() = %d, fresh %d", i, d.OutputBitDepth(), fresh.OutputBitDepth())
	}
	if d.Info() != fresh.Info() {
		t.Fatalf("step %d: Info() differs from fresh", i)
	}
	got, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("step %d: %v", i, err)
	}
	want, err := io.ReadAll(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("step %d (%d-bit, convert %d): pooled output differs from fresh", i, s.bits, s.to)
	}
}

func TestDecodeInterleavedWithConvertTo(t *testing.T) {
	stream, _ := convStream(t, 16, 2, 6000)
	streamed := decodeOpts(t, stream, WithConvertTo(32))
	got, info, err := DecodeInterleaved(bytes.NewReader(stream), WithConvertTo(32))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, streamed) {
		t.Error("one-shot converted output differs from the streaming one")
	}
	ref, _ := NewDecoder(bytes.NewReader(stream))
	if info != ref.Info() {
		t.Errorf("returned info = %+v, want native %+v", info, ref.Info())
	}
	if _, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), len(streamed), WithConvertTo(32)); err != nil {
		t.Errorf("limit == converted length: %v", err)
	}
	if _, _, err := DecodeInterleavedLimit(bytes.NewReader(stream), len(streamed)-1, WithConvertTo(32)); !errors.Is(err, ErrDecodeLimit) {
		t.Errorf("limit == converted length-1: err = %v, want ErrDecodeLimit", err)
	}
}

func TestDecodeInterleavedConvertPresize(t *testing.T) {
	stream, _ := convStream(t, 8, 1, 20000)
	run := func(to int) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, _, err := DecodeInterleaved(bytes.NewReader(stream), WithConvertTo(to)); err != nil {
				t.Fatal(err)
			}
		})
	}
	wide, identity := run(32), run(8)
	// One extra allocation is expected: the hashed path packs the native bytes first,
	// then regrows the decoder's own buffer for the wider output. Growing the result
	// buffer from a native-width hint costs at least two more.
	if wide > identity+1 {
		t.Errorf("widening decode allocates %.0f/op, identity %.0f/op; presize ignores the output width", wide, identity)
	}
}

// bngWidthConvert ports the per-sample rule of BirdNET-Go's widthConverter: decode
// each container-sized sample signed, then shift by bitDepth-16.
func bngWidthConvert(native []byte, bitDepth int) []byte {
	bps := (bitDepth + 7) / 8
	vals := unpackLE(native, bps)
	out := make([]byte, 0, len(vals)*2)
	for _, v := range vals {
		if bitDepth > 16 {
			v >>= uint(bitDepth - 16)
		} else {
			v <<= uint(16 - bitDepth)
		}
		out = append(out, byte(v), byte(v>>8))
	}
	return out
}

func TestWithConvertToMatchesBirdNETGoWidthConverter(t *testing.T) {
	for src := 4; src <= 32; src++ {
		stream, _ := convStream(t, src, 2, 5000)
		want := bngWidthConvert(decodeOpts(t, stream), src)
		got := decodeOpts(t, stream, WithConvertTo(16))
		if !bytes.Equal(got, want) {
			t.Errorf("src %d: WithConvertTo(16) differs from the widthConverter rule", src)
		}
	}
}

func TestDecodeResetReuseAllocsWithConvertTo(t *testing.T) {
	stream, _ := convStream(t, 24, 2, 12000)
	// resetAllocs measures Reset-and-decode on a warmed decoder.
	resetAllocs := func(opts ...DecoderOption) float64 {
		d, err := NewDecoder(bytes.NewReader(stream), opts...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, d); err != nil {
			t.Fatal(err)
		}
		sr := bytes.NewReader(stream)
		return testing.AllocsPerRun(30, func() {
			sr.Reset(stream)
			if err := d.Reset(sr, opts...); err != nil {
				t.Fatalf("Reset: %v", err)
			}
			if _, err := io.Copy(io.Discard, d); err != nil {
				t.Fatalf("decode: %v", err)
			}
		})
	}
	// Conversion must add no allocation over the same decode without it.
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprintf("skipMD5=%v", skip), func(t *testing.T) {
			base := []DecoderOption(nil)
			conv := []DecoderOption{WithConvertTo(16)}
			if skip {
				base = append(base, SkipMD5Verification())
				conv = append(conv, SkipMD5Verification())
			}
			want, got := resetAllocs(base...), resetAllocs(conv...)
			if got > want {
				t.Fatalf("Reset-and-decode with conversion allocates %.0f/op, %.0f/op without it", got, want)
			}
		})
	}
}

// patchStreamInfoDepth returns a copy of stream whose STREAMINFO bits-per-sample
// field is set to bits. STREAMINFO layout after "fLaC" and the 4-byte block header:
// bytes 10-12 carry the sample rate (20 bits), channels-1 (3 bits) and the high bit
// of bits-1; byte 13 carries the low 4 bits of bits-1, then the sample total.
func patchStreamInfoDepth(stream []byte, bits int) []byte {
	out := bytes.Clone(stream)
	const si = 8
	v := uint(bits - 1)
	out[si+12] = out[si+12]&^0x01 | byte(v>>4)
	out[si+13] = out[si+13]&0x0f | byte(v&0x0f)<<4
	return out
}

// TestWithConvertToUsesStreamInfoDepth pins that the shift is relative to the STREAMINFO
// depth even when the frames declare their own: a 16-bit stream whose STREAMINFO says
// 24 is packed at 3 bytes, and converting to 32 shifts it by 8, not by 16.
func TestWithConvertToUsesStreamInfoDepth(t *testing.T) {
	stream, _ := convStream(t, 16, 2, 6000)
	patched := patchStreamInfoDepth(stream, 24)
	d, err := NewDecoder(bytes.NewReader(patched), SkipMD5Verification())
	if err != nil {
		t.Fatal(err)
	}
	if d.Info().BitDepth != 24 {
		t.Fatalf("patched STREAMINFO depth = %d, want 24", d.Info().BitDepth)
	}
	native := unpackLE(decodeOpts(t, patched, SkipMD5Verification()), 3)
	got := unpackLE(decodeOpts(t, patched, SkipMD5Verification(), WithConvertTo(32)), 4)
	if len(native) == 0 || len(got) != len(native) {
		t.Fatalf("got %d samples, native %d", len(got), len(native))
	}
	for i, v := range native {
		if want := refConvert(v, 24, 32); got[i] != want {
			t.Fatalf("sample %d: native %d -> %d, want %d (shift must follow STREAMINFO depth)", i, v, got[i], want)
		}
	}
}

func TestOutputBitDepthWithoutOption(t *testing.T) {
	for _, bits := range []int{12, 24} {
		stream, _ := convStream(t, bits, 2, 2000)
		d, err := NewDecoder(bytes.NewReader(stream))
		if err != nil {
			t.Fatal(err)
		}
		if d.OutputBitDepth() != d.Info().BitDepth || d.OutputBitDepth() != bits {
			t.Errorf("%d-bit: OutputBitDepth() = %d, Info().BitDepth = %d", bits, d.OutputBitDepth(), d.Info().BitDepth)
		}
	}
}

func FuzzDecodeConvert(f *testing.F) {
	base, _ := buildRamp(2, 16, 4096*3+50)
	for sel := range uint8(4) {
		f.Add(base, sel)
	}
	f.Fuzz(func(t *testing.T, data []byte, sel uint8) {
		dst := []int{8, 16, 24, 32}[sel%4]
		d, err := NewDecoder(bytes.NewReader(data), WithConvertTo(dst))
		if err != nil {
			return
		}
		var buf bytes.Buffer
		_, _ = d.WriteTo(&buf) // an error is fine; a panic is not
		if buf.Len()%(dst/8) != 0 {
			t.Fatalf("output length %d is not a multiple of %d", buf.Len(), dst/8)
		}
	})
}

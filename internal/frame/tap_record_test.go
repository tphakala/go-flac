package frame

import (
	"bytes"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// decodeRecording decodes one frame from br while tee-ing every tapped byte into a
// recorder alongside the real CRC-8 (header) and CRC-16 (frame) updaters. It reuses
// the production header parser (readHeaderBody, which verifies CRC-8 internally) and
// the production subframe decoders, mirroring only Decode's small orchestration and
// tap handoff. Because the returned CRC-16 is folded exclusively from tapped bytes
// and is checked against the stored frame CRC-16, a byte-exact match of the recorded
// slice against the frame's source bytes proves the new tap timing reproduces the
// exact consumed-byte stream the CRCs depend on.
func decodeRecording(t *testing.T, br *bitio.Reader, si flac.StreamInfo, dst *Frame) []byte {
	t.Helper()
	var rec []byte
	var c16 uint16
	var c8 uint8
	var hdr header

	// Header tap: record + CRC-8 + CRC-16 (the frame CRC-16 covers the header too).
	br.SetTap(func(b byte) {
		rec = append(rec, b)
		c8 = crc.Update8(c8, b)
		c16 = crc.Update16(c16, b)
	})
	if err := readHeaderBody(br, si, &hdr, &c8); err != nil {
		t.Fatalf("readHeaderBody: %v", err)
	}

	// Body tap: record + CRC-16 only, reseated by SetTap to the first body byte.
	br.SetTap(func(b byte) {
		rec = append(rec, b)
		c16 = crc.Update16(c16, b)
	})

	nch := hdr.channels()
	ensureChannels(dst, nch, hdr.blockSize)
	if hdr.channelAssignment <= 7 {
		bps := hdr.bitsPerSample
		if bps >= 25 {
			scratch := make([]int64, hdr.blockSize)
			for ch := range nch {
				if err := decodeSubframe64(br, scratch, bps); err != nil {
					t.Fatalf("decodeSubframe64 ch %d: %v", ch, err)
				}
				out := dst.Channels[ch][:hdr.blockSize]
				for i, v := range scratch {
					out[i] = int32(v)
				}
			}
		} else {
			for ch := range nch {
				if err := decodeSubframe(br, dst.Channels[ch][:hdr.blockSize], bps); err != nil {
					t.Fatalf("decodeSubframe ch %d: %v", ch, err)
				}
			}
		}
	} else if err := decodeStereoDecorrelated(br, &hdr, dst); err != nil {
		t.Fatalf("decodeStereoDecorrelated: %v", err)
	}

	if err := br.SkipToByteBoundary(); err != nil {
		t.Fatalf("SkipToByteBoundary: %v", err)
	}
	computed := c16
	stored, err := br.ReadBits(16)
	if err != nil {
		t.Fatalf("read stored CRC-16: %v", err)
	}
	if stored != uint64(computed) {
		t.Fatalf("frame CRC-16 %#x != computed %#x: tap byte stream is wrong", stored, computed)
	}
	br.SetTap(nil)

	dst.BlockSize = hdr.blockSize
	dst.SampleRate = hdr.sampleRate
	dst.BitsPerSample = hdr.bitsPerSample
	dst.Number = hdr.number
	dst.VariableBlockSize = hdr.variableBlockSize
	return rec
}

// TestFrameTapRecordsExactFrameBytesConstant feeds two identical hand-built frames and
// asserts the recording tap reproduces each frame's exact bytes, with CRC-8 and CRC-16
// both verified from the tapped stream.
func TestFrameTapRecordsExactFrameBytesConstant(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	one := buildOneFrame()
	stream := append(bytes.Clone(one), one...)

	br := bitio.NewReader(bytes.NewReader(stream))
	var fr Frame
	for i := range 2 {
		rec := decodeRecording(t, br, si, &fr)
		if !bytes.Equal(rec, one) {
			t.Fatalf("frame %d: recorded tap bytes %x != frame bytes %x", i, rec, one)
		}
	}
}

// TestFrameTapRecordsExactFrameBytesEncoded encodes several frames of non-constant PCM
// (forcing Rice-coded residuals, hence real ReadUnary in the decoder) and asserts the
// recording tap reproduces each frame's exact encoded bytes, the CRC-16 verifies, and
// the decoded samples round-trip. This exercises the tap timing through unary reads,
// the header-to-body tap handoff, and the final SkipToByteBoundary + CRC-16 snapshot.
func TestFrameTapRecordsExactFrameBytesEncoded(t *testing.T) {
	si := flac.StreamInfo{SampleRate: 44100, Channels: 2, BitDepth: 16}
	const bs = 512
	p := Params{Stereo: StereoFull, MaxPartitionOrder: 4, MaxLPCOrder: 8, LPCPrecision: 14, ExhaustiveFixed: true}

	// Deterministic wandering-plus-noise PCM, bounded well within 16-bit range.
	mkChannels := func(seed uint32) ([]int32, []int32) {
		l := make([]int32, bs)
		r := make([]int32, bs)
		s := seed
		nxt := func() int32 { s = s*1664525 + 1013904223; return int32(s>>15) % 200 }
		for i := range l {
			ramp := int32(i * 6)
			l[i] = ramp + nxt() - 100
			r[i] = -ramp + nxt() - 100
		}
		return l, r
	}

	frames := make([][]byte, 0, 3)
	origL := make([][]int32, 0, 3)
	origR := make([][]int32, 0, 3)
	var stream []byte
	for f := range 3 {
		l, r := mkChannels(uint32(f)*2654435761 + 1)
		bw := bitio.NewWriter()
		enc := EncodeFrame(bw, NewWorkspace(bs, 2, 8), p, si, [][]int32{l, r}, uint64(f))
		fb := bytes.Clone(enc) // copy: enc aliases bw's buffer
		frames = append(frames, fb)
		origL = append(origL, l)
		origR = append(origR, r)
		stream = append(stream, fb...)
	}

	br := bitio.NewReader(bytes.NewReader(stream))
	var fr Frame
	for f := range 3 {
		rec := decodeRecording(t, br, si, &fr)
		if !bytes.Equal(rec, frames[f]) {
			t.Fatalf("frame %d: recorded tap bytes (len %d) != frame bytes (len %d)", f, len(rec), len(frames[f]))
		}
		if fr.BlockSize != bs || len(fr.Channels) != 2 {
			t.Fatalf("frame %d: bs=%d nch=%d", f, fr.BlockSize, len(fr.Channels))
		}
		for i := range bs {
			if fr.Channels[0][i] != origL[f][i] || fr.Channels[1][i] != origR[f][i] {
				t.Fatalf("frame %d sample %d: got (%d,%d) want (%d,%d)",
					f, i, fr.Channels[0][i], fr.Channels[1][i], origL[f][i], origR[f][i])
			}
		}
	}
}

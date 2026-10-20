package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
	"github.com/tphakala/go-flac/internal/meta"
)

const rangeFrameSamples = 16

// rangeTestFrame builds one CRC-valid 16-bit mono frame of rangeFrameSamples
// samples with frame number num. The subframe is FIXED order 0 with escape-coded
// 31-bit residuals, so each sample is stored as is and may lie outside 16 bits.
func rangeTestFrame(num int, samples []int64) []byte {
	bw := bitio.NewWriter()
	bw.WriteBits(0x3ffe, 14) // sync
	bw.WriteBits(0, 1)       // reserved
	bw.WriteBits(0, 1)       // fixed block size
	bw.WriteBits(6, 4)       // block size: 8-bit (size-1) field after the frame number
	bw.WriteBits(0, 4)       // sample rate from STREAMINFO
	bw.WriteBits(0, 4)       // mono
	bw.WriteBits(0, 3)       // bit depth from STREAMINFO
	bw.WriteBits(0, 1)       // reserved
	bw.WriteBits(uint64(num), 8)
	bw.WriteBits(rangeFrameSamples-1, 8)
	bw.WriteBits(uint64(crc.Checksum8(bw.Bytes())), 8)

	bw.WriteBits(0, 1)  // subframe padding bit
	bw.WriteBits(8, 6)  // FIXED, order 0
	bw.WriteBits(0, 1)  // no wasted bits
	bw.WriteBits(0, 2)  // Rice, 4-bit parameters
	bw.WriteBits(0, 4)  // one partition
	bw.WriteBits(15, 4) // escape: raw residuals
	bw.WriteBits(31, 5) // raw width
	for _, v := range samples {
		bw.WriteSignedBits(v, 31)
	}
	bw.AlignByte()
	bw.WriteBits(uint64(crc.Checksum16(bw.Bytes())), 16)
	return bw.Bytes()
}

// rangeTestSamples returns rangeFrameSamples samples starting at base.
func rangeTestSamples(base int64) []int64 {
	s := make([]int64, rangeFrameSamples)
	for i := range s {
		s[i] = base + int64(i)
	}
	return s
}

// pcm16 is the little-endian 16-bit packing of in-range samples.
func pcm16(s []int64) []byte {
	out := make([]byte, 0, 2*len(s))
	for _, v := range s {
		out = append(out, byte(v), byte(v>>8))
	}
	return out
}

// TestDecodeOutOfRangeFrame decodes a three-frame mono stream whose middle frame
// holds a sample outside 16 bits. The frame's CRCs are valid and the declared
// block sizes and counts add up, so only the range check can reject it.
func TestDecodeOutOfRangeFrame(t *testing.T) {
	good0 := rangeTestSamples(100)
	bad := rangeTestSamples(200)
	bad[3] = 40000
	good2 := rangeTestSamples(300)
	frames := [][]byte{rangeTestFrame(0, good0), rangeTestFrame(1, bad), rangeTestFrame(2, good2)}

	si := flac.StreamInfo{SampleRate: 44100, Channels: 1, BitDepth: 16, TotalSamples: 3 * rangeFrameSamples}
	body := meta.EncodeStreamInfo(si, rangeFrameSamples, rangeFrameSamples, 0, 0)
	var stream bytes.Buffer
	if err := meta.WriteStreamHeader(&stream, body); err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		stream.Write(f)
	}

	t.Run("Read delivers earlier frames, then a sticky sentinel", func(t *testing.T) {
		dec, err := NewDecoder(bytes.NewReader(stream.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		buf := make([]byte, 64)
		var rerr error
		for rerr == nil {
			var n int
			n, rerr = dec.Read(buf)
			got = append(got, buf[:n]...)
		}
		if !errors.Is(rerr, flac.ErrSampleOutOfRange) {
			t.Fatalf("Read error = %v, want flac.ErrSampleOutOfRange", rerr)
		}
		if errors.Is(rerr, ErrDecodeLimit) {
			t.Fatalf("Read error %v is also reported as ErrDecodeLimit", rerr)
		}
		if !bytes.Equal(got, pcm16(good0)) {
			t.Fatalf("delivered %d bytes before the error, want frame 0's %d bytes", len(got), 2*rangeFrameSamples)
		}
		n, again := dec.Read(buf)
		if n != 0 || !errors.Is(again, flac.ErrSampleOutOfRange) {
			t.Fatalf("second Read = (%d, %v), want (0, flac.ErrSampleOutOfRange)", n, again)
		}
	})

	t.Run("FrameDecoder", func(t *testing.T) {
		fd, err := NewFrameDecoder(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := fd.DecodeInterleaved(frames[1]); !errors.Is(err, flac.ErrSampleOutOfRange) {
			t.Fatalf("DecodeInterleaved error = %v, want flac.ErrSampleOutOfRange", err)
		}
		// A rejected frame leaves nothing behind: the next good frame decodes.
		out, n, err := fd.DecodeInterleaved(frames[2])
		if err != nil || n != rangeFrameSamples || !bytes.Equal(out, pcm16(good2)) {
			t.Fatalf("frame after the rejected one = (%d bytes, %d, %v), want its own samples", len(out), n, err)
		}
	})

	t.Run("seek skips the frame like a CRC failure", func(t *testing.T) {
		dec, err := NewDecoder(bytes.NewReader(stream.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		// Sample 20 lies inside the rejected frame (samples 16 to 31). The probe
		// treats the frame as a false sync and lands on frame 2, so the seek
		// reports the overshoot.
		landed, err := dec.SeekToSample(20)
		if err != nil || landed != 2*rangeFrameSamples {
			t.Fatalf("SeekToSample(20) = (%d, %v), want (%d, nil)", landed, err, 2*rangeFrameSamples)
		}
		got, err := io.ReadAll(dec)
		if err != nil || !bytes.Equal(got, pcm16(good2)) {
			t.Fatalf("Read after seek = (%d bytes, %v), want frame 2's samples", len(got), err)
		}
	})
}

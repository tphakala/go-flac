package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// readChan0 reads one inter-channel sample (channel 0) and returns its value.
func readChan0(t *testing.T, dec *Decoder, channels, bps int) int32 {
	t.Helper()
	bytesPS := (bps + 7) / 8
	buf := make([]byte, channels*bytesPS)
	if _, err := io.ReadFull(dec, buf); err != nil {
		t.Fatalf("read after seek: %v", err)
	}
	var u uint32
	for b := range bytesPS {
		u |= uint32(buf[b]) << (uint(b) * 8)
	}
	shift := 32 - bps
	return int32(u<<shift) >> shift // sign-extend from bps bits
}

// rampValue returns the channel-0 sample value that encodeRamp stores at inter-channel
// index s for the given bit depth: the low bps bits of s, sign-extended (encodeRamp
// writes byte-truncated s+c, so for bit depths narrower than the sample index range the
// stored value wraps). readChan0 applies the same sign-extension, so comparing against
// rampValue validates the landed sample. Exact sample accuracy is independently
// guaranteed by the value SeekToSample returns.
func rampValue(s int64, bps int) int32 {
	shift := 32 - bps
	return int32(uint32(s)<<shift) >> shift
}

func TestSeekSampleAccurate(t *testing.T) {
	cases := []struct{ channels, bps, samples int }{
		{1, 16, 5*4096 + 7},
		{2, 16, 5*4096 + 7},
		{2, 8, 4096 * 3},
		{2, 24, 4096*4 + 100},
	}
	for _, c := range cases {
		data, total := encodeRamp(t, c.channels, c.bps, c.samples)
		targets := []int64{0, 1, 4095, 4096, 4097, 8192, int64(total) - 1}
		for _, tg := range targets {
			dec, err := NewDecoder(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.SeekToSample(tg)
			if err != nil {
				t.Fatalf("c=%v target=%d: seek err %v", c, tg, err)
			}
			if got != tg {
				t.Fatalf("c=%v target=%d: returned %d", c, tg, got)
			}
			want := rampValue(tg, c.bps)
			if v := readChan0(t, dec, c.channels, c.bps); v != want {
				t.Fatalf("c=%v target=%d: chan0 sample = %d (want %d)", c, tg, v, want)
			}
		}
	}
}

func TestSeekPastEndKnownTotal(t *testing.T) {
	data, total := encodeRamp(t, 2, 16, 4096*2)
	dec, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.SeekToSample(int64(total) + 1000)
	if err != nil {
		t.Fatalf("seek past end err = %v", err)
	}
	if got != int64(total) {
		t.Fatalf("seek past end returned %d, want %d", got, total)
	}
	if _, err := dec.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after past-end seek = %v, want io.EOF", err)
	}
}

func TestSeekPostInvariantsNoMD5OrTruncationError(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 4096*4) // valid MD5 present
	dec, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.SeekToSample(8192); err != nil {
		t.Fatal(err)
	}
	// Reading to EOF after a seek must NOT raise ErrMD5Mismatch or ErrTruncatedStream,
	// even though only part of the stream was decoded.
	if _, err := io.Copy(io.Discard, dec); err != nil {
		t.Fatalf("post-seek read to EOF err = %v, want nil", err)
	}
}

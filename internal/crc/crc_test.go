package crc

import (
	"math/rand"
	"testing"
)

func TestCRC8Check(t *testing.T) {
	var c uint8
	for _, b := range []byte("123456789") {
		c = Update8(c, b)
	}
	if c != 0xF4 {
		t.Fatalf("CRC-8 check = %#x, want 0xF4", c)
	}
}

func TestCRC16Check(t *testing.T) {
	var c uint16
	for _, b := range []byte("123456789") {
		c = Update16(c, b)
	}
	if c != 0xFEE8 {
		t.Fatalf("CRC-16 check = %#x, want 0xFEE8", c)
	}
}

func TestChecksumHelpers(t *testing.T) {
	if got := Checksum8([]byte("123456789")); got != 0xF4 {
		t.Fatalf("Checksum8 = %#x", got)
	}
	if got := Checksum16([]byte("123456789")); got != 0xFEE8 {
		t.Fatalf("Checksum16 = %#x", got)
	}
}

func TestChecksum16SliceBy16MatchesScalar(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	// Lengths straddle the 16-byte slice-by-16 stride and its byte tail: multiples
	// of 16, one over, one under, plus large buffers with odd tails.
	for _, n := range []int{0, 1, 7, 8, 9, 15, 16, 17, 23, 31, 32, 33, 47, 48, 64, 255, 4096, 4097, 4111} {
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(r.Intn(256))
		}
		// scalarChecksum16 is the reference byte-at-a-time loop kept in the test.
		var c uint16
		for _, b := range buf {
			c = (c << 8) ^ table16[byte(c>>8)^b]
		}
		if got := Checksum16(buf); got != c {
			t.Fatalf("n=%d: Checksum16=%#04x want %#04x", n, got, c)
		}
	}
}

func BenchmarkChecksum16(b *testing.B) {
	buf := make([]byte, 16384)
	for i := range buf {
		buf[i] = byte(i)
	}
	b.SetBytes(int64(len(buf)))
	for b.Loop() {
		_ = Checksum16(buf)
	}
}

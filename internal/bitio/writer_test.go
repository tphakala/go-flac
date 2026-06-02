package bitio

import (
	"bytes"
	"testing"
)

func TestWriterPacksMSBFirst(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0b101, 3)
	w.WriteBits(0x14E, 9) // 1_0100_1110
	w.AlignByte()
	// stream MSB-first: 101 101001110 -> "10110100" "1110 0000" = 0xB4 0xE0
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xB4, 0xE0}) {
		t.Fatalf("Bytes=% x, want B4 E0", got)
	}
}

func TestWriterReaderRoundTrip(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0x3FFE, 14)
	w.WriteSignedBits(-3, 4)
	w.WriteSignedBits(7, 5)
	w.WriteUnary(0)
	w.WriteUnary(5)
	w.WriteBits(0b1011, 4)
	w.AlignByte()

	r := NewReader(bytes.NewReader(w.Bytes()))
	if v, err := r.ReadBits(14); err != nil || v != 0x3FFE {
		t.Fatalf("ReadBits(14)=%#x err=%v", v, err)
	}
	if v, err := r.ReadSigned(4); err != nil || v != -3 {
		t.Fatalf("ReadSigned(4)=%d err=%v", v, err)
	}
	if v, err := r.ReadSigned(5); err != nil || v != 7 {
		t.Fatalf("ReadSigned(5)=%d err=%v", v, err)
	}
	if v, err := r.ReadUnary(); err != nil || v != 0 {
		t.Fatalf("ReadUnary()=%d err=%v", v, err)
	}
	if v, err := r.ReadUnary(); err != nil || v != 5 {
		t.Fatalf("ReadUnary()=%d err=%v", v, err)
	}
	if v, err := r.ReadBits(4); err != nil || v != 0b1011 {
		t.Fatalf("ReadBits(4)=%#b err=%v", v, err)
	}
}

func TestWriterResetClears(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0xFF, 8)
	w.Reset()
	if len(w.Bytes()) != 0 {
		t.Fatalf("after Reset, Bytes len=%d, want 0", len(w.Bytes()))
	}
	w.WriteBits(0x01, 8)
	if got := w.Bytes(); !bytes.Equal(got, []byte{0x01}) {
		t.Fatalf("after Reset+write, Bytes=% x, want 01", got)
	}
}

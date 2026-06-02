package bitio

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadBitsMSBFirst(t *testing.T) {
	// 0b1011_0001, 0b0100_1110
	r := NewReader(bytes.NewReader([]byte{0xB1, 0x4E}))
	got, err := r.ReadBits(3) // 101
	if err != nil || got != 0b101 {
		t.Fatalf("ReadBits(3)=%b err=%v, want 101", got, err)
	}
	got, err = r.ReadBits(5) // 10001
	if err != nil || got != 0b10001 {
		t.Fatalf("ReadBits(5)=%b err=%v, want 10001", got, err)
	}
	got, err = r.ReadBits(8) // 0100_1110
	if err != nil || got != 0x4E {
		t.Fatalf("ReadBits(8)=%x err=%v, want 4e", got, err)
	}
}

func TestReadBitsZeroAndWide(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}))
	if v, err := r.ReadBits(0); err != nil || v != 0 {
		t.Fatalf("ReadBits(0)=%d err=%v, want 0", v, err)
	}
	v, err := r.ReadBits(64)
	if err != nil || v != 0xFFFFFFFFFFFFFFFF {
		t.Fatalf("ReadBits(64)=%x err=%v", v, err)
	}
}

func TestReadSignedSignExtends(t *testing.T) {
	// 4-bit value 0b1111 == -1; next 4-bit 0b0111 == 7
	r := NewReader(bytes.NewReader([]byte{0xF7}))
	if v, err := r.ReadSigned(4); err != nil || v != -1 {
		t.Fatalf("ReadSigned(4)=%d err=%v, want -1", v, err)
	}
	if v, err := r.ReadSigned(4); err != nil || v != 7 {
		t.Fatalf("ReadSigned(4)=%d err=%v, want 7", v, err)
	}
}

func TestReadUnary(t *testing.T) {
	// 0b0001_0010: unary -> 3 (three zeros then 1), then 0b0010 left:
	// after consuming 4 bits, next unary: 0b0010 -> 2 zeros then 1 -> 2
	r := NewReader(bytes.NewReader([]byte{0b0001_0010}))
	if q, err := r.ReadUnary(); err != nil || q != 3 {
		t.Fatalf("ReadUnary()=%d err=%v, want 3", q, err)
	}
	if q, err := r.ReadUnary(); err != nil || q != 2 {
		t.Fatalf("ReadUnary()=%d err=%v, want 2", q, err)
	}
}

func TestByteAlignedAndError(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xAB}))
	if !r.ByteAligned() {
		t.Fatal("fresh reader should be byte aligned")
	}
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if r.ByteAligned() {
		t.Fatal("after 4 bits should not be byte aligned")
	}
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if !r.ByteAligned() {
		t.Fatal("after 8 bits should be byte aligned")
	}
	if _, err := r.ReadBits(1); !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("past EOF want EOF-ish, got %v", err)
	}
}

func TestTapReceivesConsumedBytes(t *testing.T) {
	var tapped []byte
	r := NewReader(bytes.NewReader([]byte{0x12, 0x34, 0x56}))
	r.SetTap(func(b byte) { tapped = append(tapped, b) })
	// Consume two full bytes.
	if _, err := r.ReadBits(16); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tapped, []byte{0x12, 0x34}) {
		t.Fatalf("tap got %x, want 1234", tapped)
	}
}

func TestSkipToByteBoundary(t *testing.T) {
	var tapped []byte
	r := NewReader(bytes.NewReader([]byte{0xFF, 0x0F}))
	r.SetTap(func(b byte) { tapped = append(tapped, b) })
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if err := r.SkipToByteBoundary(); err != nil {
		t.Fatal(err)
	}
	if !r.ByteAligned() {
		t.Fatal("want aligned after skip")
	}
	if v, err := r.ReadBits(8); err != nil || v != 0x0F {
		t.Fatalf("ReadBits(8)=%x err=%v want 0f", v, err)
	}
	if !bytes.Equal(tapped, []byte{0xFF, 0x0F}) {
		t.Fatalf("tap=%x want ff0f", tapped)
	}
}

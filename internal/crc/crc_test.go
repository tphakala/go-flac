package crc

import "testing"

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

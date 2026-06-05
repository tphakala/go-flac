package crc

import simdcrc "github.com/tphakala/simd/crc"

// FLAC uses CRC-8 (poly x^8+x^2+x^1+1 = 0x07, init 0) over each frame header and
// CRC-16 (poly x^16+x^15+x^2+1 = 0x8005, init 0) over each whole frame. Both are
// MSB-first with no input/output reflection. MD5 of decoded audio uses crypto/md5
// directly in the pcm layer, so it is not re-exported here.

var (
	table8  [256]uint8
	table16 [256]uint16
)

func init() {
	for i := range 256 {
		c8 := uint8(i)
		for range 8 {
			if c8&0x80 != 0 {
				c8 = (c8 << 1) ^ 0x07
			} else {
				c8 <<= 1
			}
		}
		table8[i] = c8

		c16 := uint16(i) << 8
		for range 8 {
			if c16&0x8000 != 0 {
				c16 = (c16 << 1) ^ 0x8005
			} else {
				c16 <<= 1
			}
		}
		table16[i] = c16
	}
}

// Update8 folds one byte into a running CRC-8.
func Update8(crc, b uint8) uint8 { return table8[crc^b] }

// Update16 folds one byte into a running CRC-16.
func Update16(crc uint16, b byte) uint16 {
	return (crc << 8) ^ table16[byte(crc>>8)^b]
}

// Checksum8 returns the CRC-8 of p (init 0).
func Checksum8(p []byte) uint8 {
	var c uint8
	for _, b := range p {
		c = Update8(c, b)
	}
	return c
}

// Checksum16 returns the CRC-16 of p (init 0). It delegates to the SIMD crc
// package, which folds the buffer 16 bytes at a time with PCLMULQDQ (amd64) or
// PMULL (arm64) and falls back to the same slice-by-16 table loop on other
// architectures. The result is bit-identical to the byte-at-a-time Update16 loop.
func Checksum16(p []byte) uint16 {
	return simdcrc.Checksum16(p)
}

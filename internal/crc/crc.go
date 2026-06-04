package crc

// FLAC uses CRC-8 (poly x^8+x^2+x^1+1 = 0x07, init 0) over each frame header and
// CRC-16 (poly x^16+x^15+x^2+1 = 0x8005, init 0) over each whole frame. Both are
// MSB-first with no input/output reflection. MD5 of decoded audio uses crypto/md5
// directly in the pcm layer, so it is not re-exported here.

var (
	table8   [256]uint8
	table16  [256]uint16
	table16x [8][256]uint16
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

	// Slice-by-8 derived tables: table16x[0] == table16; table16x[n][b] is the
	// CRC-16 of a byte b followed by n zero bytes, so eight input bytes can be
	// folded in one step. MSB-first, no reflection, matching Update16.
	for b := range 256 {
		table16x[0][b] = table16[b]
	}
	for n := 1; n < 8; n++ {
		for b := range 256 {
			prev := table16x[n-1][b]
			table16x[n][b] = (prev << 8) ^ table16[byte(prev>>8)]
		}
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

// Checksum16 returns the CRC-16 of p (init 0), folding 8 bytes per iteration via
// the slice-by-8 tables; bit-identical to the byte-at-a-time Update16 loop.
func Checksum16(p []byte) uint16 {
	var c uint16
	for len(p) >= 8 {
		c = table16x[7][byte(c>>8)^p[0]] ^
			table16x[6][byte(c)^p[1]] ^
			table16x[5][p[2]] ^
			table16x[4][p[3]] ^
			table16x[3][p[4]] ^
			table16x[2][p[5]] ^
			table16x[1][p[6]] ^
			table16x[0][p[7]]
		p = p[8:]
	}
	for _, b := range p {
		c = (c << 8) ^ table16[byte(c>>8)^b]
	}
	return c
}

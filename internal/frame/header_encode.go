package frame

import (
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// writeFrameHeader writes a fixed-blocksize frame header (sync .. CRC-8) into bw,
// which must be byte aligned at entry. Sample rate and bit depth are coded as
// "get from STREAMINFO" (code 0). It is the inverse of readHeaderBody.
func writeFrameHeader(bw *bitio.Writer, blockSize, chCode int, frameNum uint64) {
	start := len(bw.Bytes())
	bw.WriteBits(syncCode, 14)
	bw.WriteBits(0, 1) // reserved
	bw.WriteBits(0, 1) // blocking strategy: fixed
	bsCode, extN, extV := blockSizeCode(blockSize)
	bw.WriteBits(uint64(bsCode), 4)
	bw.WriteBits(0, 4) // sample rate: get from STREAMINFO
	bw.WriteBits(uint64(chCode), 4)
	bw.WriteBits(0, 3) // bps: get from STREAMINFO
	bw.WriteBits(0, 1) // reserved
	writeUTF8(bw, frameNum)
	if extN > 0 {
		bw.WriteBits(uint64(extV), uint(extN))
	}
	// At this point the writer is byte aligned (sync+reserved+bs+sr+ch+bps+reserved = 32 bits,
	// UTF-8 bytes are whole bytes, extension bytes are whole bytes). CRC-8 covers
	// all header bytes written since start.
	hdr := bw.Bytes()[start:]
	bw.WriteBits(uint64(crc.Checksum8(hdr)), 8)
}

// blockSizeCode returns the 4-bit blocksize code and, when the code requires an
// explicit blocksize-1 field at the end of the header, its width in bits (8 or 16)
// and value. Mirrors decodeBlockSize.
func blockSizeCode(bs int) (code, extN, extV int) {
	switch bs {
	case 192:
		return 1, 0, 0
	case 576:
		return 2, 0, 0
	case 1152:
		return 3, 0, 0
	case 2304:
		return 4, 0, 0
	case 4608:
		return 5, 0, 0
	case 256:
		return 8, 0, 0
	case 512:
		return 9, 0, 0
	case 1024:
		return 10, 0, 0
	case 2048:
		return 11, 0, 0
	case 4096:
		return 12, 0, 0
	case 8192:
		return 13, 0, 0
	case 16384:
		return 14, 0, 0
	case 32768:
		return 15, 0, 0
	}
	if bs-1 <= 0xFF {
		return 6, 8, bs - 1
	}
	return 7, 16, bs - 1
}

// writeUTF8 writes v using FLAC's extended UTF-8 coded-number form (1..7 bytes,
// up to 36 bits). It is the inverse of readCodedNumber.
func writeUTF8(bw *bitio.Writer, v uint64) {
	if v < 0x80 {
		bw.WriteBits(v, 8)
		return
	}
	var n int
	switch {
	case v < 0x800:
		n = 2
	case v < 0x10000:
		n = 3
	case v < 0x200000:
		n = 4
	case v < 0x4000000:
		n = 5
	case v < 0x80000000:
		n = 6
	default:
		n = 7
	}
	// Lead byte: n high bits set to 1, then a 0, then (7-n) data bits.
	lead := byte(0xFF << (8 - uint(n)))
	hi := byte(v >> uint(6*(n-1)))
	bw.WriteBits(uint64(lead|(hi&^lead)), 8)
	for i := n - 2; i >= 0; i-- {
		six := byte((v >> uint(6*i)) & 0x3F)
		bw.WriteBits(uint64(0x80|six), 8)
	}
}

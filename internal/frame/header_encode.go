package frame

import (
	"fmt"

	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
)

// writeFrameHeader writes a fixed-blocksize frame header (sync .. CRC-8) into bw,
// which must be byte aligned at entry. The sample rate and bit depth are written
// with their explicit FLAC codes whenever one exists, so the stream is
// self-describing rather than relying on the "read from STREAMINFO" code 0.
// Strict decoders (notably Apple CoreAudio, used by iOS/macOS Safari's <audio>)
// reject code 0 and fail to play the stream, while libFLAC/ffmpeg/Chrome/Firefox
// tolerate it; emitting explicit codes is what every mainstream encoder does.
// Rates or depths with no dedicated code fall back to 0. It is the inverse of
// readHeaderBody.
func writeFrameHeader(bw *bitio.Writer, blockSize, chCode, sampleRate, bitDepth int, frameNum uint64) {
	start := len(bw.Bytes())
	bw.WriteBits(syncCode, 14)
	bw.WriteBits(0, 1) // reserved
	bw.WriteBits(0, 1) // blocking strategy: fixed
	bsCode, bsExtN, bsExtV := blockSizeCode(blockSize)
	srCode, srExtN, srExtV := sampleRateCode(sampleRate)
	bw.WriteBits(uint64(bsCode), 4)
	bw.WriteBits(uint64(srCode), 4)
	bw.WriteBits(uint64(chCode), 4)
	bw.WriteBits(uint64(bitDepthCode(bitDepth)), 3)
	bw.WriteBits(0, 1) // reserved
	writeUTF8(bw, frameNum)
	// Explicit blocksize/sample-rate values, when present, follow the coded number,
	// blocksize first then sample rate, matching the decoder's read order in
	// decodeBlockSize then decodeSampleRate.
	if bsExtN > 0 {
		bw.WriteBits(uint64(bsExtV), uint(bsExtN))
	}
	if srExtN > 0 {
		bw.WriteBits(uint64(srExtV), uint(srExtN))
	}
	// The writer is byte aligned here (the fixed fields total 32 bits and both the
	// UTF-8 coded number and the extension values are whole bytes). CRC-8 covers
	// all header bytes written since start.
	hdr := bw.Bytes()[start:]
	bw.WriteBits(uint64(crc.Checksum8(hdr)), 8)
}

// sampleRateCode returns the 4-bit frame-header sample-rate code and, when the
// code carries an explicit value at the end of the header, that value's width in
// bits (8 or 16) and the value itself. Standard rates use a direct code (1..11);
// other rates use an escape form (12 = kHz in 8 bits, 13 = Hz in 16 bits,
// 14 = tens of Hz in 16 bits) when they fit, falling back to 0 ("read from
// STREAMINFO") otherwise. Mirrors decodeSampleRate.
func sampleRateCode(sr int) (code, extN, extV int) {
	// pcm.NewEncoder validates the sample rate (1..655350) before any frame is
	// written, so a non-positive value here is a programming error. Fail fast
	// rather than silently coding it as "read from STREAMINFO".
	if sr <= 0 {
		panic(fmt.Sprintf("go-flac: sampleRateCode: non-positive sample rate %d", sr))
	}
	switch sr {
	case 88200:
		return 1, 0, 0
	case 176400:
		return 2, 0, 0
	case 192000:
		return 3, 0, 0
	case 8000:
		return 4, 0, 0
	case 16000:
		return 5, 0, 0
	case 22050:
		return 6, 0, 0
	case 24000:
		return 7, 0, 0
	case 32000:
		return 8, 0, 0
	case 44100:
		return 9, 0, 0
	case 48000:
		return 10, 0, 0
	case 96000:
		return 11, 0, 0
	}
	switch {
	case sr%1000 == 0 && sr/1000 <= 0xFF:
		return 12, 8, sr / 1000
	case sr <= 0xFFFF:
		return 13, 16, sr
	case sr%10 == 0 && sr/10 <= 0xFFFF:
		return 14, 16, sr / 10
	}
	// A valid rate with no dedicated code and outside the escape ranges (e.g. an
	// odd rate above 65535 Hz): the FLAC spec's only option is code 0, which tells
	// the decoder to read the rate from STREAMINFO.
	return 0, 0, 0
}

// bitDepthCode returns the 3-bit frame-header sample-size code for bps, or 0
// ("read from STREAMINFO") for depths with no dedicated code. Mirrors
// decodeBitsPerSample.
func bitDepthCode(bps int) int {
	switch bps {
	case 8:
		return 1
	case 12:
		return 2
	case 16:
		return 4
	case 20:
		return 5
	case 24:
		return 6
	case 32:
		return 7
	}
	return 0
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

package pcm

import "github.com/tphakala/go-flac/internal/crc"

type bitWriter struct {
	buf []byte
	acc uint32
	nb  uint
}

func (w *bitWriter) put(v uint64, n uint) {
	for i := int(n) - 1; i >= 0; i-- {
		w.acc = (w.acc << 1) | uint32((v>>uint(i))&1)
		w.nb++
		if w.nb == 8 {
			w.buf = append(w.buf, byte(w.acc))
			w.acc, w.nb = 0, 0
		}
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nb > 0 {
		return append(w.buf, byte(w.acc<<(8-w.nb)))
	}
	return w.buf
}

func buildStreamInfo() []byte {
	out := []byte("fLaC")
	// Last metadata block (0x80), type 0 (STREAMINFO), length 34 (0x000022).
	out = append(out, 0x80, 0x00, 0x00, 0x22)
	body := make([]byte, 0, 34)
	put16 := func(v int) { body = append(body, byte(v>>8), byte(v)) }
	put24 := func(v int) { body = append(body, byte(v>>16), byte(v>>8), byte(v)) }
	put16(2)    // min block size
	put16(4096) // max block size
	put24(0)    // min frame size
	put24(0)    // max frame size
	// 20-bit sample rate | 3-bit (channels-1) | 5-bit (bps-1) | 36-bit total samples
	// packed into 64 bits, written big-endian.
	var packed uint64
	packed = uint64(44100) << 44
	packed |= uint64(2-1) << 41
	packed |= uint64(16-1) << 36
	packed |= 0 // total samples 0
	for i := 7; i >= 0; i-- {
		body = append(body, byte(packed>>(uint(i)*8)))
	}
	body = append(body, make([]byte, 16)...) // MD5 all zero: skip check
	return append(out, body...)
}

func buildFrame() []byte {
	// Frame header: sync 0xFF 0xF8, bsCode=7 srCode=0 (0x70), chAssign=1 bpsCode=0 (0x10),
	// frame number 0x00, blocksize-1 as 16-bit 0x0001, then CRC-8.
	hdrBody := []byte{0xFF, 0xF8, 0x70, 0x10, 0x00, 0x00, 0x01}
	hdr := make([]byte, len(hdrBody)+1)
	copy(hdr, hdrBody)
	hdr[len(hdrBody)] = crc.Checksum8(hdrBody)

	w := &bitWriter{}
	// channel 0: CONSTANT subframe, value 100 (16-bit)
	w.put(0, 1) // zero-pad bit
	w.put(0, 6) // subframe type CONSTANT = 0
	w.put(0, 1) // wasted bits flag
	w.put(uint64(uint16(100)), 16)
	// channel 1: CONSTANT subframe, value -100 (16-bit two's complement)
	w.put(0, 1)
	w.put(0, 6)
	w.put(0, 1)
	neg := int16(-100)
	w.put(uint64(uint16(neg)), 16) // 0xFF9C
	body := w.bytes()

	// Concatenate header + subframe body, then append CRC-16.
	full := make([]byte, 0, len(hdr)+len(body)+2)
	full = append(full, hdr...)
	full = append(full, body...)
	c16 := crc.Checksum16(full)
	return append(full, byte(c16>>8), byte(c16))
}

func buildStream() []byte {
	return append(buildStreamInfo(), buildFrame()...)
}

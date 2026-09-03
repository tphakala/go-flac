package pcm

import "encoding/binary"

// deinterleaveSamples fills ch[c][:blockSize] from src, which must hold exactly
// blockSize*nch*bytesPS bytes of interleaved little-endian two's-complement PCM.
// It is the exact inverse of appendPacked, so re-packing the result reproduces src.
//
// 16-bit PCM (CD audio, the 48 kHz capture path) is the overwhelmingly common
// case, so mono and stereo at that width take widened-load fast paths; every
// other width and channel count uses the generic byte unpacker. The fast paths
// produce identical int32 values to the generic path on little-endian hosts,
// which is every architecture this package targets.
func deinterleaveSamples(ch [][]int32, src []byte, blockSize, nch, bytesPS int) {
	if bytesPS == 2 {
		switch nch {
		case 1:
			deinterleave16Mono(ch[0], src, blockSize)
			return
		case 2:
			deinterleave16Stereo(ch[0], ch[1], src, blockSize)
			return
		}
	}
	deinterleaveSamplesGeneric(ch, src, blockSize, nch, bytesPS)
}

// deinterleave16Mono fills dst[:n] from n consecutive little-endian int16
// samples in src, sign-extending each to int32. It reads four samples per 64-bit
// load so the compiler emits one widened move instead of eight byte loads, then
// finishes the remainder one sample at a time. Reslicing src to its exact length
// up front lets the compiler prove the indexed loads are in range.
func deinterleave16Mono(dst []int32, src []byte, n int) {
	dst = dst[:n]
	src = src[:2*n]
	i := 0
	for ; i+4 <= n; i += 4 {
		w := binary.LittleEndian.Uint64(src[2*i:])
		dst[i] = int32(int16(w))
		dst[i+1] = int32(int16(w >> 16))
		dst[i+2] = int32(int16(w >> 32))
		dst[i+3] = int32(int16(w >> 48))
	}
	for ; i < n; i++ {
		dst[i] = int32(int16(binary.LittleEndian.Uint16(src[2*i:])))
	}
}

// deinterleave16Stereo splits 2*n interleaved little-endian int16 samples
// (L0,R0,L1,R1,...) into left[:n] and right[:n], sign-extending each to int32.
// It consumes two stereo frames per 64-bit load and handles a final odd frame
// with a 32-bit-load tail.
func deinterleave16Stereo(left, right []int32, src []byte, n int) {
	left = left[:n]
	right = right[:n]
	src = src[:4*n]
	i := 0
	for ; i+2 <= n; i += 2 {
		w := binary.LittleEndian.Uint64(src[4*i:])
		left[i] = int32(int16(w))
		right[i] = int32(int16(w >> 16))
		left[i+1] = int32(int16(w >> 32))
		right[i+1] = int32(int16(w >> 48))
	}
	for ; i < n; i++ {
		w := binary.LittleEndian.Uint32(src[4*i:])
		left[i] = int32(int16(w))
		right[i] = int32(int16(w >> 16))
	}
}

// deinterleaveSamplesGeneric is the byte-at-a-time unpacker for the sample
// widths and channel counts the fast paths above do not cover.
func deinterleaveSamplesGeneric(ch [][]int32, src []byte, blockSize, nch, bytesPS int) {
	idx := 0
	switch bytesPS {
	case 1:
		for i := range blockSize {
			for c := range nch {
				ch[c][i] = int32(int8(src[idx]))
				idx++
			}
		}
	case 2:
		for i := range blockSize {
			for c := range nch {
				ch[c][i] = int32(int16(uint16(src[idx]) | uint16(src[idx+1])<<8))
				idx += 2
			}
		}
	case 3:
		for i := range blockSize {
			for c := range nch {
				u := uint32(src[idx]) | uint32(src[idx+1])<<8 | uint32(src[idx+2])<<16
				if u&0x800000 != 0 {
					u |= 0xFF000000 // sign-extend bit 23
				}
				ch[c][i] = int32(u)
				idx += 3
			}
		}
	default: // 4
		for i := range blockSize {
			for c := range nch {
				ch[c][i] = int32(uint32(src[idx]) | uint32(src[idx+1])<<8 |
					uint32(src[idx+2])<<16 | uint32(src[idx+3])<<24)
				idx += 4
			}
		}
	}
}

// packLE16Mono writes src[:n] as n consecutive little-endian int16 samples into
// dst, four samples per 64-bit store so the compiler emits one widened move
// instead of eight byte writes, then finishes the remainder one sample at a time.
// It is the exact inverse of deinterleave16Mono and the widened analogue of
// appendPacked's scalar 16-bit mono store. dst must hold at least 2*n bytes.
//
// Each sample is truncated to its low 16 bits via a uint16 cast BEFORE widening,
// matching the scalar store: without that cast a negative int32 would sign-extend
// into the neighboring sample's bytes. The shifts and PutUint64 are value-based
// and little-endian on every architecture, so the bytes are identical to the
// scalar store on all hosts, not only little-endian ones.
func packLE16Mono(dst []byte, src []int32, n int) {
	dst = dst[:2*n]
	src = src[:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		w := uint64(uint16(src[i])) | uint64(uint16(src[i+1]))<<16 |
			uint64(uint16(src[i+2]))<<32 | uint64(uint16(src[i+3]))<<48
		binary.LittleEndian.PutUint64(dst[2*i:], w)
	}
	for ; i < n; i++ {
		binary.LittleEndian.PutUint16(dst[2*i:], uint16(src[i]))
	}
}

// packLE16Stereo interleaves left[:n] and right[:n] as little-endian int16 pairs
// (L0,R0,L1,R1,...) into dst, two stereo frames per 64-bit store, with a 32-bit
// store for a final odd frame. It is the exact inverse of deinterleave16Stereo and
// the widened analogue of appendPacked's scalar 16-bit stereo store. dst must hold
// at least 4*n bytes. The uint16 casts truncate before widening for the same
// sign-bleed reason as packLE16Mono, so output matches the scalar store on all
// architectures.
func packLE16Stereo(dst []byte, left, right []int32, n int) {
	dst = dst[:4*n]
	left = left[:n]
	right = right[:n]
	i := 0
	for ; i+2 <= n; i += 2 {
		w := uint64(uint16(left[i])) | uint64(uint16(right[i]))<<16 |
			uint64(uint16(left[i+1]))<<32 | uint64(uint16(right[i+1]))<<48
		binary.LittleEndian.PutUint64(dst[4*i:], w)
	}
	for ; i < n; i++ {
		binary.LittleEndian.PutUint32(dst[4*i:], uint32(uint16(left[i]))|uint32(uint16(right[i]))<<16)
	}
}

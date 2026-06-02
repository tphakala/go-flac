package pcm

// deinterleaveSamples fills ch[c][:blockSize] from src, which must hold exactly
// blockSize*nch*bytesPS bytes of interleaved little-endian two's-complement PCM.
// It is the exact inverse of appendPacked, so re-packing the result reproduces src.
func deinterleaveSamples(ch [][]int32, src []byte, blockSize, nch, bytesPS int) {
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

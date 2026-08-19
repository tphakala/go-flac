package frame

import "github.com/tphakala/go-flac/internal/i32"

// Inter-channel decorrelation inverses. Each function writes len(...) samples into
// outL and outR, which must not alias the inputs (guaranteed at the call sites in
// decodeStereoDecorrelated: the subframes decode into separate work32 scratch and
// the results go to distinct channel buffers).
//
// The int32 functions dispatch to the SIMD i32 kernels (AVX2/NEON/pure Go). They
// use int32 wraparound, which is bit-identical to the int64 form below for every
// valid stream: the int32 path is only chosen for bps <= 24 (frame.go), so mid is
// at most 24 bits, side at most 25, and the mid/side intermediate at most 26,
// leaving 5 bits of headroom in a signed int32. The int64 *64 variants below serve
// the wide bps >= 25 path, where those bounds no longer hold.

func decorrelateLeftSide(left, side, outL, outR []int32) {
	copy(outL[:len(left)], left) // outL = left
	i32.Sub(outR, left, side)    // outR = left - side
}

func decorrelateRightSide(side, right, outL, outR []int32) {
	copy(outR[:len(right)], right) // outR = right
	i32.Add(outL, right, side)     // outL = right + side
}

func decorrelateMidSide(mid, side, outL, outR []int32) {
	// (mid<<1)|(side&1) recovers the dropped low bit, then outL=(sum+side)>>1,
	// outR=(sum-side)>>1, matching the int64 form for the bps <= 24 int32 path.
	i32.MidSideDecode(outL, outR, mid, side)
}

func decorrelateLeftSide64(left, side []int64, outL, outR []int32) {
	for i := range left {
		outL[i] = int32(left[i])
		outR[i] = int32(left[i] - side[i])
	}
}

func decorrelateRightSide64(side, right []int64, outL, outR []int32) {
	for i := range right {
		outL[i] = int32(right[i] + side[i])
		outR[i] = int32(right[i])
	}
}

func decorrelateMidSide64(mid, side []int64, outL, outR []int32) {
	for i := range mid {
		m := (mid[i] << 1) | (side[i] & 1)
		outL[i] = int32((m + side[i]) >> 1)
		outR[i] = int32((m - side[i]) >> 1)
	}
}

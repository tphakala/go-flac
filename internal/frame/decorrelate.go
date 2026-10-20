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
// leaving 5 bits of headroom in a signed int32. That headroom holds while the
// subframes are within their widths, which decodeStereoDecorrelated checks before
// calling them; the callers then check the computed channels against the frame
// depth. The int64 *64 variants below serve the wide bps >= 25 path, where those
// bounds no longer hold.

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

// The int64 variants serve the wide path. Each computes a derived channel in
// int64, tests it against [-2^(bps-1), 2^(bps-1)) and only then narrows it to
// int32, because the narrowing alone could wrap an out-of-range value back into
// range. They return false when any derived sample is out of range, in which case
// the contents of outL and outR are unspecified. The channel that is a subframe
// itself (left in left/side, right in right/side) was range-checked as a subframe.

func decorrelateLeftSide64(left, side []int64, outL, outR []int32, bps int) bool {
	lim := int64(1) << uint(bps-1)
	bad := false
	for i := range left {
		r := left[i] - side[i]
		bad = bad || r < -lim || r >= lim
		outL[i] = int32(left[i])
		outR[i] = int32(r)
	}
	return !bad
}

func decorrelateRightSide64(side, right []int64, outL, outR []int32, bps int) bool {
	lim := int64(1) << uint(bps-1)
	bad := false
	for i := range right {
		l := right[i] + side[i]
		bad = bad || l < -lim || l >= lim
		outL[i] = int32(l)
		outR[i] = int32(right[i])
	}
	return !bad
}

func decorrelateMidSide64(mid, side []int64, outL, outR []int32, bps int) bool {
	lim := int64(1) << uint(bps-1)
	bad := false
	for i := range mid {
		m := (mid[i] << 1) | (side[i] & 1)
		l := (m + side[i]) >> 1
		r := (m - side[i]) >> 1
		bad = bad || l < -lim || l >= lim || r < -lim || r >= lim
		outL[i] = int32(l)
		outR[i] = int32(r)
	}
	return !bad
}

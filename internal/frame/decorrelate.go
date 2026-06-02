package frame

// Inter-channel decorrelation inverses. All arithmetic is int64 so 32-bps side
// channels (33-bit) and mid/side (34-bit intermediates) never overflow; results
// fit int32. Each function writes len(...) samples into outL and outR.

func decorrelateLeftSide(left, side, outL, outR []int32) {
	for i := range left {
		l := int64(left[i])
		s := int64(side[i])
		outL[i] = int32(l)
		outR[i] = int32(l - s)
	}
}

func decorrelateRightSide(side, right, outL, outR []int32) {
	for i := range right {
		r := int64(right[i])
		s := int64(side[i])
		outL[i] = int32(r + s)
		outR[i] = int32(r)
	}
}

func decorrelateMidSide(mid, side, outL, outR []int32) {
	for i := range mid {
		m := int64(mid[i])
		s := int64(side[i])
		m = (m << 1) | (s & 1)
		outL[i] = int32((m + s) >> 1)
		outR[i] = int32((m - s) >> 1)
	}
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

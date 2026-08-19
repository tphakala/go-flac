package frame

import (
	"math/rand"
	"testing"
)

func TestDecorrelateLeftSide(t *testing.T) {
	// left=[10,20], side=left-right -> right=left-side.
	left := []int32{10, 20}
	side := []int32{3, -4}
	gotL, gotR := make([]int32, 2), make([]int32, 2)
	decorrelateLeftSide(left, side, gotL, gotR)
	wantR := []int32{7, 24}
	for i := range left {
		if gotL[i] != left[i] || gotR[i] != wantR[i] {
			t.Fatalf("i=%d L=%d R=%d wantR=%d", i, gotL[i], gotR[i], wantR[i])
		}
	}
}

func TestDecorrelateMidSide(t *testing.T) {
	// left=11, right=7 -> mid=(11+7)>>1=9, side=11-7=4.
	// reconstruct: mid2 = (mid<<1)|(side&1)=18|0=18; left=(18+4)/2=11; right=(18-4)/2=7.
	mid := []int32{9}
	side := []int32{4}
	gotL, gotR := make([]int32, 1), make([]int32, 1)
	decorrelateMidSide(mid, side, gotL, gotR)
	if gotL[0] != 11 || gotR[0] != 7 {
		t.Fatalf("L=%d R=%d want 11 7", gotL[0], gotR[0])
	}
}

// TestDecorrelateSIMDMatchesScalar proves the SIMD int32 decorrelation inverses
// are bit-identical to the int64 reference (decorrelate*64) and reconstruct the
// original channels, across the full bps <= 24 int32-path envelope (left/right in
// [-2^23, 2^23), side up to 25 bits) that the int32 path is actually used for.
// Both odd and even left+right parities are covered so the mid/side low-bit
// recovery is exercised, including the range extremes.
func TestDecorrelateSIMDMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9e37))
	const lim = 1 << 23 // 24-bit signed sample magnitude
	sizes := []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 33, 4096}
	for _, n := range sizes {
		left := make([]int32, n)
		right := make([]int32, n)
		for i := range left {
			left[i] = int32(rng.Intn(2*lim) - lim)
			right[i] = int32(rng.Intn(2*lim) - lim)
		}
		if n >= 4 { // seed range extremes and both parities
			left[0], right[0] = lim-1, -lim   // even sum
			left[1], right[1] = lim-1, -lim+1 // odd sum
			left[2], right[2] = -lim, lim-1   // odd sum
			left[3], right[3] = 0, 0
		}

		// Encode to the three decorrelation layouts (as the FLAC encoder does).
		side := make([]int32, n)
		mid := make([]int32, n)
		side64 := make([]int64, n)
		mid64 := make([]int64, n)
		left64 := make([]int64, n)
		right64 := make([]int64, n)
		for i := range left {
			side[i] = left[i] - right[i]
			mid[i] = (left[i] + right[i]) >> 1
			side64[i] = int64(side[i])
			mid64[i] = int64(mid[i])
			left64[i] = int64(left[i])
			right64[i] = int64(right[i])
		}

		type mode struct {
			name   string
			simd   func(oL, oR []int32)
			scalar func(oL, oR []int32)
		}
		modes := []mode{
			{"left/side",
				func(oL, oR []int32) { decorrelateLeftSide(left, side, oL, oR) },
				func(oL, oR []int32) { decorrelateLeftSide64(left64, side64, oL, oR) }},
			{"right/side",
				func(oL, oR []int32) { decorrelateRightSide(side, right, oL, oR) },
				func(oL, oR []int32) { decorrelateRightSide64(side64, right64, oL, oR) }},
			{"mid/side",
				func(oL, oR []int32) { decorrelateMidSide(mid, side, oL, oR) },
				func(oL, oR []int32) { decorrelateMidSide64(mid64, side64, oL, oR) }},
		}
		for _, m := range modes {
			simdL, simdR := make([]int32, n), make([]int32, n)
			scL, scR := make([]int32, n), make([]int32, n)
			m.simd(simdL, simdR)
			m.scalar(scL, scR)
			for i := range left {
				if simdL[i] != scL[i] || simdR[i] != scR[i] {
					t.Fatalf("n=%d %s[%d]: simd=(%d,%d) scalar=(%d,%d)",
						n, m.name, i, simdL[i], simdR[i], scL[i], scR[i])
				}
				if simdL[i] != left[i] || simdR[i] != right[i] {
					t.Fatalf("n=%d %s[%d]: decoded=(%d,%d) want original=(%d,%d)",
						n, m.name, i, simdL[i], simdR[i], left[i], right[i])
				}
			}
		}
	}
}

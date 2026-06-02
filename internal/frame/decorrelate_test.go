package frame

import "testing"

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

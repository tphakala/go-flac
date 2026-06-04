package rice

import (
	"bytes"
	"math"
	"math/rand"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// encodeThenDecode round-trips residuals through EncodeResidual/DecodeResidual.
func encodeThenDecode(t *testing.T, res []int32, blockSize, predOrder, maxPO int) {
	t.Helper()
	bw := bitio.NewWriter()
	var sc Scratch
	EncodeResidual(bw, res, blockSize, predOrder, maxPO, &sc)
	bw.AlignByte()

	got := make([]int32, blockSize)
	br := bitio.NewReader(bytes.NewReader(bw.Bytes()))
	if err := DecodeResidual(br, got, blockSize, predOrder); err != nil {
		t.Fatalf("DecodeResidual: %v", err)
	}
	for i := predOrder; i < blockSize; i++ {
		if got[i] != res[i-predOrder] {
			t.Fatalf("residual[%d]=%d, want %d", i, got[i], res[i-predOrder])
		}
	}
}

func TestRiceRoundTripSmall(t *testing.T) {
	res := []int32{0, 1, -1, 2, -2, 100, -100, 0, 0, 0, 5, -3, 7, -9, 11, -13}
	encodeThenDecode(t, res, 16, 0, 4)
}

func TestRiceRoundTripPredOrder(t *testing.T) {
	res := make([]int32, 30)
	for i := range res {
		res[i] = int32(i%7 - 3)
	}
	encodeThenDecode(t, res, 32, 2, 4)
}

func TestRiceRoundTripLargeMagnitude(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	res := make([]int32, 4096)
	for i := range res {
		res[i] = rng.Int31n(1<<25) - (1 << 24)
	}
	for po := 0; po <= 6; po++ {
		encodeThenDecode(t, res, 4096, 0, po)
	}
}

func TestRiceRoundTripConstantZero(t *testing.T) {
	encodeThenDecode(t, make([]int32, 64), 64, 0, 3)
}

// TestRiceBitsNoInt32Overflow pins the Rice cost accumulator at int64 width. A
// partition of large zigzag values coded at k=0 sums past 2^32, which a 32-bit
// int accumulator (GOARCH=386) would wrap, causing bestParam to pick a wrong
// parameter. Comparing the result directly against an int64 want both requires the
// int64 return type (compile-time guard) and catches the wrap at runtime on 386.
func TestRiceBitsNoInt32Overflow(t *testing.T) {
	const n = 4096
	const each = uint64(1) << 20 // each entry contributes (each>>0)+1+0 bits at k=0
	zz := make([]uint64, n)
	for i := range zz {
		zz[i] = each
	}
	want := int64(n) * int64(each+1)
	if want <= math.MaxInt32 {
		t.Fatalf("test design error: want %d must exceed math.MaxInt32 to exercise the 32-bit overflow", want)
	}
	if got := riceBits(zz, 0); got != want {
		t.Fatalf("riceBits(zz, 0) = %d, want %d (32-bit accumulator overflow?)", got, want)
	}
}

// TestFeasiblePmaxClampsTo15 pins the partition-order ceiling at 15. The FLAC
// partition-order field is 4 bits, so feasiblePmax must never return an order
// above 15 even when the caller passes a larger maxPartOrder and the block is
// divisible by a higher power of two. Without the clamp, WritePlanned would
// write the chosen order into the 4-bit field with bw.WriteBits(po, 4) and
// truncate it (e.g. 16 -> 0), corrupting the stream. paramsForLevel only ever
// sets 3..6 today, so this guards a forward-looking config path.
func TestFeasiblePmaxClampsTo15(t *testing.T) {
	cases := []struct {
		blockSize, predOrder, maxPartOrder, want int
	}{
		{65536, 0, 20, 15},   // 2^16 divisible: unclamped feasiblePmax would be 16
		{1 << 20, 0, 25, 15}, // 2^20 divisible: unclamped would be 20
		{1 << 18, 4, 18, 15}, // partLen at po=15 is 8 >= predOrder 4; unclamped would be 16
		{4096, 0, 6, 6},      // normal level-3 case: clamp does not change it
		{4096, 0, 20, 12},    // 4096 = 2^12, so divisibility caps pmax at 12 below the clamp
	}
	for _, c := range cases {
		if got := feasiblePmax(c.blockSize, c.predOrder, c.maxPartOrder); got != c.want {
			t.Errorf("feasiblePmax(%d, %d, %d) = %d, want %d",
				c.blockSize, c.predOrder, c.maxPartOrder, got, c.want)
		}
	}
}

func TestCostMatchesWrittenBits(t *testing.T) {
	res := []int32{0, 5, -5, 12, -12, 3, -3, 9}
	bw := bitio.NewWriter()
	var sc Scratch
	written := EncodeResidual(bw, res, 8, 0, 2, &sc)
	if got := CostResidual(res, 8, 0, 2, &sc); got != written {
		t.Fatalf("CostResidual=%d, EncodeResidual wrote=%d", got, written)
	}
}

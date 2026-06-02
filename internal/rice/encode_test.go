package rice

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// encodeThenDecode round-trips residuals through EncodeResidual/DecodeResidual.
func encodeThenDecode(t *testing.T, res []int32, blockSize, predOrder, maxPO int) {
	t.Helper()
	bw := bitio.NewWriter()
	EncodeResidual(bw, res, blockSize, predOrder, maxPO)
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

func TestCostMatchesWrittenBits(t *testing.T) {
	res := []int32{0, 5, -5, 12, -12, 3, -3, 9}
	bw := bitio.NewWriter()
	written := EncodeResidual(bw, res, 8, 0, 2)
	if got := CostResidual(res, 8, 0, 2); got != written {
		t.Fatalf("CostResidual=%d, EncodeResidual wrote=%d", got, written)
	}
}

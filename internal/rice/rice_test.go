package rice

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// buildResidual encodes one partition-order-0 residual block by hand for testing.
// method=0 (4-bit param). Each value is zigzag+rice encoded with parameter k.
func buildResidualBits() (data []byte, want []int32) {
	// We will encode residuals [0, -1, 1, -2] with k=0 so remainder is empty and
	// the value is pure unary of the zigzag mapping: zz(0)=0, zz(-1)=1, zz(1)=2,
	// zz(-2)=3. Unary(q) = q zeros then a 1.
	// Header: method(2)=00, partitionOrder(4)=0000, param(4)=0000 -> 10 bits.
	// Then unary codes: 1, 01, 001, 0001.
	var bits []byte
	var acc uint32
	var nb uint
	put := func(v uint32, n uint) {
		for i := int(n) - 1; i >= 0; i-- {
			acc = (acc << 1) | ((v >> uint(i)) & 1)
			nb++
			if nb == 8 {
				bits = append(bits, byte(acc))
				acc, nb = 0, 0
			}
		}
	}
	put(0, 2) // method 0
	put(0, 4) // partition order 0
	put(0, 4) // rice param 0
	put(0b1, 1)
	put(0b01, 2)
	put(0b001, 3)
	put(0b0001, 4)
	if nb > 0 {
		bits = append(bits, byte(acc<<(8-nb)))
	}
	return bits, []int32{0, -1, 1, -2}
}

func TestDecodeResidualOrder0(t *testing.T) {
	data, want := buildResidualBits()
	br := bitio.NewReader(bytes.NewReader(data))
	blockSize := len(want)
	dst := make([]int32, blockSize)
	if err := DecodeResidual(br, dst, blockSize, 0); err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], want[i])
		}
	}
}

func TestDecodeResidualEscapePartition(t *testing.T) {
	// method 1 (5-bit param), partition order 0, param=escape5 (31), raw bits=4.
	// residuals: 4-bit signed raw values 0b0111=7, 0b1000=-8.
	var bits []byte
	var acc uint32
	var nb uint
	put := func(v uint32, n uint) {
		for i := int(n) - 1; i >= 0; i-- {
			acc = (acc << 1) | ((v >> uint(i)) & 1)
			nb++
			if nb == 8 {
				bits = append(bits, byte(acc))
				acc, nb = 0, 0
			}
		}
	}
	put(1, 2)  // method 1
	put(0, 4)  // partition order 0
	put(31, 5) // escape param
	put(4, 5)  // raw bit width 4
	put(0b0111, 4)
	put(0b1000, 4)
	if nb > 0 {
		bits = append(bits, byte(acc<<(8-nb)))
	}
	br := bitio.NewReader(bytes.NewReader(bits))
	dst := make([]int32, 2)
	if err := DecodeResidual(br, dst, 2, 0); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 7 || dst[1] != -8 {
		t.Fatalf("dst=%v want [7 -8]", dst)
	}
}

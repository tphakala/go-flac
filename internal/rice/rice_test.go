package rice

import (
	"bytes"
	"errors"
	"testing"

	flac "github.com/tphakala/go-flac"
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

// limitSymbol is the symbol under test of a TestDecodeResidualRiceLimit row.
type limitSymbol struct {
	name    string
	fiveBit bool   // 5-bit parameter method (k up to 30) instead of 4-bit (k up to 14)
	k       uint   // Rice parameter
	q, r    uint64 // quotient and remainder, so the folded value is q<<k | r
	escape  bool   // the partition is an escape partition holding raw 31-bit -2^30
	second  bool   // the symbol sits in partition 1 instead of partition 0
	want    int64  // decoded residual when accepted
	reject  bool
}

// at is the index of the sample the symbol under test decodes to.
func (s limitSymbol) at() int {
	if s.second {
		return 1
	}
	return 0
}

// writeLimitResidual builds a two-sample, predictor-order-0 residual with partition
// order 1. The symbol under test fills one partition (partition 1 when s.second);
// the other holds one legal symbol (parameter 0, quotient 3, value -2). A marker
// byte follows the residual.
func writeLimitResidual(s limitSymbol) []byte {
	bw := bitio.NewWriter()
	paramBits, method := uint(4), uint64(0)
	if s.fiveBit || s.escape {
		paramBits, method = 5, 1
	}
	bw.WriteBits(method, 2)
	bw.WriteBits(1, 4) // two partitions
	legal := func() {
		bw.WriteBits(0, paramBits)
		bw.WriteUnary(3)
	}
	if s.second {
		legal()
	}
	if s.escape {
		bw.WriteBits(escape5, paramBits)
		bw.WriteBits(31, 5) // raw width
		bw.WriteSignedBits(-(1 << 30), 31)
	} else {
		bw.WriteBits(uint64(s.k), paramBits)
		bw.WriteUnary(s.q)
		bw.WriteBits(s.r, s.k)
	}
	if !s.second {
		legal()
	}
	bw.WriteBits(0xA5, 8)
	bw.AlignByte()
	return bw.Bytes()
}

func checkRiceLimit[T Sample](t *testing.T, s limitSymbol) {
	t.Helper()
	br := bitio.NewReader(bytes.NewReader(writeLimitResidual(s)))
	dst := make([]T, 2)
	err := DecodeResidual(br, dst, 2, 0)
	if s.reject {
		if !errors.Is(err, ErrResidualRange) || !errors.Is(err, flac.ErrSampleOutOfRange) {
			t.Fatalf("DecodeResidual error = %v, want ErrResidualRange wrapping flac.ErrSampleOutOfRange", err)
		}
	} else {
		if err != nil {
			t.Fatalf("DecodeResidual: %v", err)
		}
		if got := int64(dst[s.at()]); got != s.want {
			t.Errorf("dst[%d] = %d, want %d", s.at(), got, s.want)
		}
	}
	// The whole residual is read before the verdict returns, so the other
	// partition's symbol and the following bits are intact.
	if other := dst[1-s.at()]; other != -2 {
		t.Errorf("dst[%d] = %d, want -2", 1-s.at(), other)
	}
	if m, err := br.ReadBits(8); err != nil || m != 0xA5 {
		t.Errorf("marker = %#x, %v; want 0xa5 (reader not left at the residual's end)", m, err)
	}
}

// TestDecodeResidualRiceLimit pins the largest folded Rice value DecodeResidual
// accepts, u = 2^32-1 (residual -2^31), and the smallest it rejects, u = 2^32,
// for both sample widths and for both parameter methods.
func TestDecodeResidualRiceLimit(t *testing.T) {
	rows := []limitSymbol{
		{name: "k30 largest positive", fiveBit: true, k: 30, q: 3, r: 1<<30 - 2, want: 1<<31 - 1},
		{name: "k30 edge -2^31", fiveBit: true, k: 30, q: 3, r: 1<<30 - 1, want: -(1 << 31)},
		{name: "k30 first positive past the edge", fiveBit: true, k: 30, q: 4, r: 0, reject: true},
		{name: "k30 first negative past the edge", fiveBit: true, k: 30, q: 4, r: 1, reject: true},
		{name: "k14 edge -2^31", k: 14, q: 1<<18 - 1, r: 1<<14 - 1, want: -(1 << 31)},
		{name: "k14 first past the edge", k: 14, q: 1 << 18, r: 0, reject: true},
		{name: "k30 edge -2^31 in partition 1", fiveBit: true, k: 30, q: 3, r: 1<<30 - 1, second: true, want: -(1 << 31)},
		{name: "k30 first past the edge in partition 1", fiveBit: true, k: 30, q: 4, r: 0, second: true, reject: true},
		{name: "escape partition 31 bits", escape: true, want: -(1 << 30)},
	}
	for _, s := range rows {
		t.Run(s.name+"/int32", func(t *testing.T) { checkRiceLimit[int32](t, s) })
		t.Run(s.name+"/int64", func(t *testing.T) { checkRiceLimit[int64](t, s) })
	}
}

// TestHighBitsSeesShiftWrap pins that the bound is taken from the quotient before
// the shift. Both quotients make q<<k wrap to exactly 0 in uint64, so a test of
// the shifted value would see a legal residual. A stream that reaches such a
// quotient needs at least 2 GiB of zero bits, so DecodeResidual cannot cover it.
func TestHighBitsSeesShiftWrap(t *testing.T) {
	for _, c := range []struct {
		q uint64
		k uint
	}{{1 << 34, 30}, {1 << 50, 14}} {
		if c.q<<c.k != 0 {
			t.Fatalf("q=%d k=%d: q<<k = %d, the row no longer wraps", c.q, c.k, c.q<<c.k)
		}
		if highBits(c.q, c.k) == 0 {
			t.Errorf("highBits(%d, %d) = 0, want nonzero", c.q, c.k)
		}
	}
}

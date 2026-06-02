package frame

import (
	"bytes"
	"testing"

	"github.com/tphakala/go-flac/internal/bitio"
)

// bitWriter is a tiny MSB-first writer for building subframe fixtures.
type bitWriter struct {
	buf []byte
	acc uint32
	nb  uint
}

func (w *bitWriter) put(v uint64, n uint) {
	for i := int(n) - 1; i >= 0; i-- {
		w.acc = (w.acc << 1) | uint32((v>>uint(i))&1)
		w.nb++
		if w.nb == 8 {
			w.buf = append(w.buf, byte(w.acc))
			w.acc, w.nb = 0, 0
		}
	}
}
func (w *bitWriter) bytes() []byte {
	if w.nb > 0 {
		return append(w.buf, byte(w.acc<<(8-w.nb)))
	}
	return w.buf
}

func TestSubframeConstant(t *testing.T) {
	// header byte: 0 (pad) + 000000 (constant) + 0 (no wasted) = 0x00.
	w := &bitWriter{}
	w.put(0, 1)                       // padding
	w.put(0, 6)                       // type constant
	w.put(0, 1)                       // no wasted bits
	w.put(uint64(uint16(0x0123)), 16) // constant value, 16-bit
	br := bitio.NewReader(bytes.NewReader(w.bytes()))
	dst := make([]int32, 4)
	if err := decodeSubframe(br, dst, 16); err != nil {
		t.Fatal(err)
	}
	for i := range dst {
		if dst[i] != 0x0123 {
			t.Fatalf("dst[%d]=%d want 0x123", i, dst[i])
		}
	}
}

func TestSubframeVerbatim(t *testing.T) {
	w := &bitWriter{}
	w.put(0, 1)
	w.put(1, 6) // type verbatim
	w.put(0, 1)
	vals := []int32{1, -2, 3, -4}
	for _, v := range vals {
		w.put(uint64(uint16(int16(v))), 16)
	}
	br := bitio.NewReader(bytes.NewReader(w.bytes()))
	dst := make([]int32, len(vals))
	if err := decodeSubframe(br, dst, 16); err != nil {
		t.Fatal(err)
	}
	for i := range vals {
		if dst[i] != vals[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], vals[i])
		}
	}
}

func TestSubframeWastedBits(t *testing.T) {
	// Constant subframe with 2 wasted bits: stored value 0x48, restored << 2.
	w := &bitWriter{}
	w.put(0, 1)
	w.put(0, 6)                       // constant
	w.put(1, 1)                       // wasted bits present
	w.put(0b01, 2)                    // unary: one 0 then 1 -> k-1=1 -> k=2 wasted bits
	w.put(uint64(uint16(0x48)), 16-2) // value stored in bps-wasted = 14 bits
	br := bitio.NewReader(bytes.NewReader(w.bytes()))
	dst := make([]int32, 2)
	if err := decodeSubframe(br, dst, 16); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 0x48<<2 {
		t.Fatalf("dst[0]=%d want %d", dst[0], 0x48<<2)
	}
}

func TestSubframeFixedOrder2(t *testing.T) {
	// Fixed order 2 over a linear ramp 0,1,2,3 -> residuals all 0.
	// header: pad 0, type 0b001010 (=10 -> fixed order 2), wasted 0.
	w := &bitWriter{}
	w.put(0, 1)
	w.put(10, 6) // fixed order 2
	w.put(0, 1)
	w.put(uint64(uint16(0)), 16) // warmup s0=0
	w.put(uint64(uint16(1)), 16) // warmup s1=1
	// residual: method 0, partition order 0, param 0, two unary zeros: zz(0)=0 -> "1","1"
	w.put(0, 2)
	w.put(0, 4)
	w.put(0, 4)
	w.put(1, 1)
	w.put(1, 1)
	br := bitio.NewReader(bytes.NewReader(w.bytes()))
	dst := make([]int32, 4)
	if err := decodeSubframe(br, dst, 16); err != nil {
		t.Fatal(err)
	}
	want := []int32{0, 1, 2, 3}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d]=%d want %d", i, dst[i], want[i])
		}
	}
}

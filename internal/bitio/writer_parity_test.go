package bitio

import (
	"bytes"
	"math/rand"
	"testing"
)

// bitOracle is a trivially correct MSB-first bit packer used as an independent
// reference for Writer. It records every written bit in order, then packs to
// bytes on demand. Its packing logic shares nothing with Writer's accumulator,
// so a parity match proves Writer produces the exact FLAC-stream bytes.
type bitOracle struct {
	bits []byte // one entry per bit (0 or 1), in MSB-first write order
}

func (o *bitOracle) writeBits(v uint64, n uint) {
	v &= (uint64(1) << n) - 1
	for i := int(n) - 1; i >= 0; i-- {
		o.bits = append(o.bits, byte((v>>uint(i))&1))
	}
}

func (o *bitOracle) writeSigned(v int64, n uint) {
	o.writeBits(uint64(v)&((uint64(1)<<n)-1), n)
}

func (o *bitOracle) writeUnary(q uint64) {
	for ; q > 0; q-- {
		o.bits = append(o.bits, 0)
	}
	o.bits = append(o.bits, 1)
}

func (o *bitOracle) align() {
	for len(o.bits)%8 != 0 {
		o.bits = append(o.bits, 0)
	}
}

// completeBytes packs only the whole-byte prefix of the written bits, mirroring
// Writer.Bytes() when a partial (<8 bit) tail is still pending.
func (o *bitOracle) completeBytes() []byte {
	nbytes := len(o.bits) / 8
	out := make([]byte, nbytes)
	for i := range out {
		var b byte
		for j := range 8 {
			b = (b << 1) | o.bits[i*8+j]
		}
		out[i] = b
	}
	return out
}

// TestWriterMatchesOracleByteAligned drives Writer and the oracle through the
// same randomized op stream and compares Bytes() at every byte-aligned point and
// after a final AlignByte. Widths stay within the documented 0..57 range.
func TestWriterMatchesOracleByteAligned(t *testing.T) {
	rng := rand.New(rand.NewSource(0xF1AC))
	for iter := range 200 {
		w := NewWriter()
		var o bitOracle
		nbits := 0
		ops := rng.Intn(400) + 50
		for k := range ops {
			switch rng.Intn(4) {
			case 0: // WriteBits, width 0..57
				n := uint(rng.Intn(58))
				v := rng.Uint64()
				w.WriteBits(v, n)
				o.writeBits(v, n)
				nbits += int(n)
			case 1: // WriteSignedBits, width 1..33
				n := uint(rng.Intn(33) + 1)
				v := int64(rng.Uint64()) >> (64 - n) // sign-extended n-bit value
				w.WriteSignedBits(v, n)
				o.writeSigned(v, n)
				nbits += int(n)
			case 2: // WriteUnary, quotient 0..200 (crosses 32/64 chunking)
				q := uint64(rng.Intn(201))
				w.WriteUnary(q)
				o.writeUnary(q)
				nbits += int(q) + 1
			case 3: // mid-stream Bytes() must match at any time for the whole-byte prefix
				if got, want := w.Bytes(), o.completeBytes(); !bytes.Equal(got, want) {
					t.Fatalf("iter %d op %d: mid-stream Bytes()=% x, want % x", iter, k, got, want)
				}
			}
		}
		w.AlignByte()
		o.align()
		if got, want := w.Bytes(), o.completeBytes(); !bytes.Equal(got, want) {
			t.Fatalf("iter %d (nbits=%d): aligned Bytes()=% x, want % x", iter, nbits, got, want)
		}
	}
}

// TestWriterBytesKeepsWritingAfterPartialFlush pins the continuation contract:
// calling Bytes() while a partial byte is pending must return the complete-byte
// prefix and leave the pending bits intact so later writes still land correctly.
func TestWriterBytesKeepsWritingAfterPartialFlush(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0xABCDE, 20) // 2 whole bytes + 4 pending bits
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xAB, 0xCD}) {
		t.Fatalf("after 20 bits, Bytes()=% x, want AB CD", got)
	}
	// Calling Bytes() again must be stable.
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xAB, 0xCD}) {
		t.Fatalf("repeat Bytes()=% x, want AB CD", got)
	}
	w.WriteBits(0x7, 4) // completes the third byte: low nibble E then 7 -> 0xE7
	w.AlignByte()
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xAB, 0xCD, 0xE7}) {
		t.Fatalf("final Bytes()=% x, want AB CD E7", got)
	}
}

// TestWriterWordBoundarySpanning forces writes that straddle the 64-bit
// accumulator boundary at every starting offset, then round-trips through Reader.
func TestWriterWordBoundarySpanning(t *testing.T) {
	for offset := uint(0); offset <= 57; offset++ {
		for width := uint(1); width <= 57; width++ {
			w := NewWriter()
			var o bitOracle
			if offset > 0 {
				w.WriteBits(0x1FFFFFFFFFFFFFF&0xA5A5A5A5A5A5A5, offset)
				o.writeBits(0x1FFFFFFFFFFFFFF&0xA5A5A5A5A5A5A5, offset)
			}
			v := uint64(0x123456789ABCDEF)
			w.WriteBits(v, width)
			o.writeBits(v, width)
			w.AlignByte()
			o.align()
			if got, want := w.Bytes(), o.completeBytes(); !bytes.Equal(got, want) {
				t.Fatalf("offset=%d width=%d: Bytes()=% x, want % x", offset, width, got, want)
			}
		}
	}
}

// TestWriterUnaryRoundTrip checks WriteUnary across the 32-bit chunk boundaries
// it uses internally, verified by reading the value back.
func TestWriterUnaryRoundTrip(t *testing.T) {
	for _, q := range []uint64{0, 1, 7, 31, 32, 33, 63, 64, 65, 95, 96, 127, 200, 1000} {
		w := NewWriter()
		w.WriteUnary(q)
		w.AlignByte()
		r := NewReader(bytes.NewReader(w.Bytes()))
		if got, err := r.ReadUnary(); err != nil || got != q {
			t.Fatalf("WriteUnary(%d) round-trip got %d err %v", q, got, err)
		}
	}
}

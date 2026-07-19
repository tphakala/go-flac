package bitio

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// bitReadOracle is a trivially correct MSB-first bit reader used as an independent
// reference for Reader. It stores the source bytes and a bit cursor and mirrors the
// Reader's documented EOF vs truncation semantics. Its extraction logic (one bit at
// a time) shares nothing with the accumulator math, so a parity match proves the
// accumulator returns exactly the FLAC-stream bits.
type bitReadOracle struct {
	data []byte
	pos  int // bit cursor: number of bits consumed so far
}

func newBitReadOracle(data []byte) *bitReadOracle {
	return &bitReadOracle{data: data}
}

func (o *bitReadOracle) total() int { return len(o.data) * 8 }

func (o *bitReadOracle) bitAt(i int) uint64 {
	return uint64((o.data[i>>3] >> (7 - uint(i&7))) & 1)
}

func (o *bitReadOracle) readBits(n uint) (uint64, error) {
	if n == 0 {
		return 0, nil
	}
	avail := o.total() - o.pos
	if avail < int(n) {
		if avail == 0 {
			return 0, io.EOF // clean end at a byte boundary
		}
		return 0, io.ErrUnexpectedEOF // some bits present but fewer than requested
	}
	var v uint64
	for i := uint(0); i < n; i++ {
		v = (v << 1) | o.bitAt(o.pos)
		o.pos++
	}
	return v, nil
}

func (o *bitReadOracle) readSigned(n uint) (int64, error) {
	if n == 0 {
		return 0, nil
	}
	u, err := o.readBits(n)
	if err != nil {
		return 0, err
	}
	shift := 64 - n
	return int64(u<<shift) >> shift, nil
}

func (o *bitReadOracle) readUnary() (uint64, error) {
	var zeros uint64
	for o.pos < o.total() {
		b := o.bitAt(o.pos)
		o.pos++
		if b == 1 {
			return zeros, nil
		}
		zeros++
	}
	// Ran off the end with only zeros: the Reader consumes every remaining bit and
	// returns a zero count with raw io.EOF (no ErrUnexpectedEOF translation here).
	return 0, io.EOF
}

func (o *bitReadOracle) skipToByteBoundary() {
	if rem := o.pos % 8; rem != 0 {
		o.pos += 8 - rem
	}
}

func (o *bitReadOracle) byteAligned() bool { return o.pos%8 == 0 }
func (o *bitReadOracle) bytesRead() int64  { return int64(o.pos / 8) }

// sameErrKind reports whether two errors are the same kind for the Reader contract:
// both nil, both io.ErrUnexpectedEOF, or both io.EOF (and not ErrUnexpectedEOF).
func sameErrKind(a, b error) bool {
	kind := func(e error) int {
		switch {
		case e == nil:
			return 0
		case errors.Is(e, io.ErrUnexpectedEOF):
			return 1
		case errors.Is(e, io.EOF):
			return 2
		default:
			return 3
		}
	}
	return kind(a) == kind(b)
}

// FuzzReaderOracle runs the same randomized op script against the real Reader and the
// bit-by-bit oracle and asserts identical values, identical EOF error kinds, and
// matching alignment/consumed-byte accounting. It is the strongest regression net for
// the accumulator math. The script stops at the first error (both readers behave
// identically up to that point), and BytesRead is compared only at byte boundaries,
// where the value is exact for every reader design.
func FuzzReaderOracle(f *testing.F) {
	f.Add([]byte{0x00}, uint16(0))
	f.Add([]byte{0xFF, 0xAB, 0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}, uint16(0xB1A5))
	f.Add(make([]byte, 40), uint16(0x1234))                      // long zero runs for ReadUnary
	f.Add([]byte{0x12, 0x00, 0x00, 0x00, 0x80, 0xFF}, uint16(7)) // unary spanning bytes then bits
	f.Fuzz(func(t *testing.T, data []byte, seed uint16) {
		r := NewReader(bytes.NewReader(data))
		o := newBitReadOracle(data)
		rng := uint32(seed)*2654435761 + 1
		next := func() uint32 { rng = rng*1664525 + 1013904223; return rng >> 8 }
		for step := range 400 {
			var gerr, oerr error
			switch next() % 4 {
			case 0:
				n := uint(next() % 65) // 0..64
				var gv, ov uint64
				gv, gerr = r.ReadBits(n)
				ov, oerr = o.readBits(n)
				if !sameErrKind(gerr, oerr) || gv != ov {
					t.Fatalf("step %d ReadBits(%d): got (%#x,%v) oracle (%#x,%v)", step, n, gv, gerr, ov, oerr)
				}
			case 1:
				n := uint(next()%64) + 1 // 1..64
				var gv, ov int64
				gv, gerr = r.ReadSigned(n)
				ov, oerr = o.readSigned(n)
				if !sameErrKind(gerr, oerr) || gv != ov {
					t.Fatalf("step %d ReadSigned(%d): got (%d,%v) oracle (%d,%v)", step, n, gv, gerr, ov, oerr)
				}
			case 2:
				var gv, ov uint64
				gv, gerr = r.ReadUnary()
				ov, oerr = o.readUnary()
				if !sameErrKind(gerr, oerr) || gv != ov {
					t.Fatalf("step %d ReadUnary: got (%d,%v) oracle (%d,%v)", step, gv, gerr, ov, oerr)
				}
			case 3:
				if err := r.SkipToByteBoundary(); err != nil {
					t.Fatalf("step %d SkipToByteBoundary err=%v", step, err)
				}
				o.skipToByteBoundary()
			}
			if gerr != nil || oerr != nil {
				break // both readers diverge only in post-error state, which decode never uses
			}
			if r.ByteAligned() != o.byteAligned() {
				t.Fatalf("step %d ByteAligned mismatch: got %v oracle %v", step, r.ByteAligned(), o.byteAligned())
			}
			if o.byteAligned() && r.BytesRead() != o.bytesRead() {
				t.Fatalf("step %d BytesRead mismatch: got %d oracle %d", step, r.BytesRead(), o.bytesRead())
			}
		}
	})
}

package bitio

import (
	"encoding/binary"
	"slices"
)

// Writer packs bits MSB-first into an in-memory byte buffer. It is the inverse of
// Reader. Each frame is assembled in full, then its CRC fields are computed over
// Bytes() and appended, so no CRC tap is needed on the write side.
//
// Bits accumulate MSB-first in a 64-bit register and are flushed to the buffer a
// whole 8-byte word at a time, keeping the per-bit cost off the byte-store path.
// Complete bytes still pending in the register are flushed lazily by Bytes() and
// AlignByte(), so callers that read Bytes() at a byte boundary (the frame-header
// CRC-8 and frame CRC-16 taps) observe the full byte stream.
type Writer struct {
	buf  []byte
	acc  uint64 // pending bits, left-aligned: valid bits occupy the top nbit positions
	nbit uint   // number of valid pending bits, 0..63
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Grow reserves capacity for at least n more bytes beyond the writer's current
// length, without changing len(buf) or touching the pending bit accumulator.
// A caller that knows a safe upper bound on the bytes it is about to write (for
// example, a frame encoder sizing the writer for one block before encoding it)
// should call Grow with that bound once at the start of the work, so
// flushWord's repeated 8-byte appends land in already-reserved capacity
// instead of paying append's growth bookkeeping, or reallocating, on every
// full word. Grow never truncates or otherwise mutates buf's existing
// contents; it is purely a capacity hint, so it cannot change the bytes a
// later Bytes() call returns. n <= 0 is a no-op.
func (w *Writer) Grow(n int) {
	if n <= 0 {
		return
	}
	w.buf = slices.Grow(w.buf, n)
}

// WriteBits writes the low n bits of v, most-significant bit first. n must be in
// 0..57 (existing callers write at most 36, the STREAMINFO total-samples field).
// Bits above bit n-1 of v are ignored.
func (w *Writer) WriteBits(v uint64, n uint) {
	if n == 0 {
		return
	}
	v &= (uint64(1) << n) - 1
	free := 64 - w.nbit
	if n < free {
		w.acc |= v << (free - n)
		w.nbit += n
		return
	}
	// The top `free` bits of v complete the 64-bit word; flush it, then stage the
	// remaining low (n-free) bits at the top of a fresh register.
	w.acc |= v >> (n - free)
	w.flushWord()
	rem := n - free
	w.nbit = rem
	if rem > 0 {
		w.acc = v << (64 - rem)
	}
}

// flushWord appends the full 64-bit accumulator as 8 big-endian bytes and clears
// it. It is only called when the register is exactly full. AppendUint64 writes the
// 8 bytes in one step (into Grow-reserved capacity when the caller pre-grew bw),
// avoiding the separate zero-fill then overwrite that a plain append followed by
// PutUint64 would do.
func (w *Writer) flushWord() {
	w.buf = binary.BigEndian.AppendUint64(w.buf, w.acc)
	w.acc = 0
}

// flushFull moves every complete byte pending in the accumulator into buf, leaving
// the partial (<8 bit) tail staged at the top of the register so writing can
// continue without disturbing the bitstream.
func (w *Writer) flushFull() {
	for w.nbit >= 8 {
		w.buf = append(w.buf, byte(w.acc>>56))
		w.acc <<= 8
		w.nbit -= 8
	}
}

// WriteSignedBits writes v as an n-bit two's-complement value (the inverse of
// Reader.ReadSigned). v must fit in the signed n-bit range.
func (w *Writer) WriteSignedBits(v int64, n uint) {
	w.WriteBits(uint64(v)&((uint64(1)<<n)-1), n)
}

// WriteUnary writes q zero bits followed by a terminating 1 bit (the inverse of
// Reader.ReadUnary).
func (w *Writer) WriteUnary(q uint64) {
	for q >= 32 {
		w.WriteBits(0, 32)
		q -= 32
	}
	// q zeros then a 1 == the value 1 written in (q+1) bits.
	w.WriteBits(1, uint(q)+1)
}

// AlignByte zero-pads to the next byte boundary. After it, Bytes() is byte-exact.
func (w *Writer) AlignByte() {
	w.flushFull()
	if w.nbit > 0 {
		w.buf = append(w.buf, byte(w.acc>>56))
		w.acc = 0
		w.nbit = 0
	}
}

// Bytes flushes any whole bytes still pending in the accumulator and returns the
// assembled bytes. It is only byte-exact when the writer is byte aligned
// (AlignByte called, or only whole bytes written); a sub-byte tail stays pending.
func (w *Writer) Bytes() []byte {
	w.flushFull()
	return w.buf
}

// Reset clears the writer for reuse, retaining the backing array.
func (w *Writer) Reset() {
	w.buf = w.buf[:0]
	w.acc = 0
	w.nbit = 0
}

package bitio

import (
	"bufio"
	"errors"
	"io"
)

// Reader reads bits MSB-first from an underlying byte source, the bit order FLAC
// uses. It buffers input (via bufio unless the source is already an io.ByteReader)
// so callers need not pre-buffer. A tap, if set, receives every fully consumed
// source byte in order, which the frame decoder uses to feed CRC hashers.
type Reader struct {
	src  io.ByteReader
	tap  func(byte)
	cur  byte // current partial byte
	nbit uint // number of valid low bits remaining in cur (0..8)
	err  error
}

// NewReader returns a Reader over r. If r already implements io.ByteReader it is
// used directly; otherwise it is wrapped in a bufio.Reader.
func NewReader(r io.Reader) *Reader {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &Reader{src: br}
}

// SetTap registers fn to be called with every fully consumed source byte.
func (r *Reader) SetTap(fn func(byte)) { r.tap = fn }

// ByteAligned reports whether the next bit starts a fresh byte.
func (r *Reader) ByteAligned() bool { return r.nbit == 0 }

func (r *Reader) loadByte() error {
	if r.err != nil {
		return r.err
	}
	b, err := r.src.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		r.err = err
		return err
	}
	r.cur = b
	r.nbit = 8
	return nil
}

// ReadBits reads n bits (0..64) MSB-first and returns them right-aligned.
func (r *Reader) ReadBits(n uint) (uint64, error) {
	var out uint64
	for n > 0 {
		if r.nbit == 0 {
			if err := r.loadByte(); err != nil {
				return 0, err
			}
		}
		take := n
		if take > r.nbit {
			take = r.nbit
		}
		// Top `take` bits of the `nbit` valid low bits of cur.
		shift := r.nbit - take
		mask := byte((1 << take) - 1)
		bits := (r.cur >> shift) & mask
		out = (out << take) | uint64(bits)
		r.nbit -= take
		n -= take
		if r.nbit == 0 && r.tap != nil {
			r.tap(r.cur)
		}
	}
	return out, nil
}

// ReadSigned reads n bits and sign-extends them as a two's-complement integer.
func (r *Reader) ReadSigned(n uint) (int64, error) {
	if n == 0 {
		return 0, nil
	}
	u, err := r.ReadBits(n)
	if err != nil {
		return 0, err
	}
	shift := 64 - n
	return int64(u<<shift) >> shift, nil
}

// ReadUnary counts zero bits up to and including the terminating one bit and
// returns the count of zeros.
func (r *Reader) ReadUnary() (uint64, error) {
	var zeros uint64
	for {
		if r.nbit == 0 {
			if err := r.loadByte(); err != nil {
				return 0, err
			}
		}
		// Scan the valid low bits of cur from the top.
		for r.nbit > 0 {
			bit := (r.cur >> (r.nbit - 1)) & 1
			r.nbit--
			if r.nbit == 0 && r.tap != nil {
				r.tap(r.cur)
			}
			if bit == 1 {
				return zeros, nil
			}
			zeros++
		}
	}
}

// SkipToByteBoundary discards remaining bits in the current partial byte so the
// next read starts on a byte boundary. The skipped byte is tapped if it had been
// partially consumed.
func (r *Reader) SkipToByteBoundary() error {
	if r.nbit == 0 {
		return nil
	}
	if r.tap != nil {
		r.tap(r.cur)
	}
	r.nbit = 0
	return nil
}

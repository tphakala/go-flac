package bitio

import (
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
)

// readBlock is the owned read-buffer size. It is large enough to amortize the
// underlying src.Read and feed the 64-bit bulk-load fast path many consecutive
// whole words, yet small enough to stay L1/L2 friendly.
const readBlock = 1 << 13 // 8 KiB

// maxEmptyReads bounds consecutive (0, nil) reads from a pathological source, the
// same guard bufio.Reader applies, so readMore cannot spin forever.
const maxEmptyReads = 100

// Reader reads bits MSB-first from an underlying byte source, the bit order FLAC
// uses. It owns all buffering: bytes are pulled from src into an 8 KiB block and
// shifted into a 64-bit accumulator, so there is no per-byte interface dispatch.
// A tap, if set, receives every fully consumed source byte in order, which the
// frame decoder uses to feed CRC hashers.
//
// Bit layout is LEFT-ALIGNED, MSB-first: the nbits valid bits occupy the TOP
// nbits positions of acc (bits [64-nbits, 63]); all lower bits are zero. This
// makes acc>>(64-n) a mask-free extractor and bits.LeadingZeros64(acc) a correct
// zero-run scan.
type Reader struct {
	src     io.Reader // raw source; the Reader owns all buffering
	buf     []byte    // owned read block; buf[:w] are valid bytes read from src
	r       int       // load cursor: count of buf bytes already shifted into acc
	w       int       // valid-byte count in buf (buf[:w] valid)
	acc     uint64    // bit accumulator, left-aligned: next bit to serve is bit 63
	nbits   uint      // number of valid bits currently in acc, 0..64
	tap     func(byte)
	tapCur  int   // buf index of the next byte to hand to tap (consumption cursor)
	loaded  int64 // cumulative bytes ever shifted from buf into acc (never reset)
	basePos int64 // absolute byte-offset seed (NewReaderAt); 0 for NewReader
	err     error // sticky; once set, stays set
}

// NewReader returns a Reader over r. The Reader reads directly from r in blocks
// into its own buffer; r need not be buffered or implement io.ByteReader.
func NewReader(r io.Reader) *Reader {
	return &Reader{src: r, buf: make([]byte, readBlock)}
}

// NewReaderAt returns a Reader over r whose byte counter starts at pos. Use it
// after seeking the underlying source to an absolute offset, so BytesRead stays
// absolute.
func NewReaderAt(r io.Reader, pos int64) *Reader {
	br := NewReader(r)
	br.basePos = pos
	return br
}

// Reset re-points the Reader at src and clears all read state, reusing the owned
// buffer so a caller that processes many short sources (the frame resync scan,
// which tries decoding at each candidate offset) does not reallocate the 8 KiB
// block each time. The tap is cleared and basePos reset to 0; reinstall a tap
// with SetTap if one is needed.
func (r *Reader) Reset(src io.Reader) {
	buf := r.buf
	if cap(buf) >= readBlock {
		buf = buf[:readBlock] // reuse the existing backing array
	} else {
		buf = make([]byte, readBlock)
	}
	*r = Reader{src: src, buf: buf}
}

// SetTap registers fn to be called with every fully consumed source byte. It
// reseats the consumption cursor so only bytes consumed WHILE fn is installed are
// tapped.
func (r *Reader) SetTap(fn func(byte)) {
	if fn != nil {
		r.tapCur = r.consumedBytes() // next byte to be consumed
	}
	r.tap = fn
}

// ByteAligned reports whether the next bit starts a fresh byte. The accumulator
// can hold several whole buffered bytes, so alignment is nbits&7==0, not nbits==0.
func (r *Reader) ByteAligned() bool { return r.nbits&7 == 0 }

// BytesRead returns the number of whole bytes consumed from the source, seeded by
// basePos. It is exact at a byte boundary (nbits&7==0), which is the only time
// callers read it (frame and metadata boundaries).
func (r *Reader) BytesRead() int64 {
	return r.basePos + ((r.loaded*8 - int64(r.nbits)) >> 3)
}

// consumedBytes returns the number of fully-consumed source bytes in the current
// buf coordinates. It is a cheap, inlinable helper the hot read paths use as a
// gate before calling emitTaps, and readMore/SetTap use for cursor math.
func (r *Reader) consumedBytes() int { return (r.r*8 - int(r.nbits)) >> 3 }

// readMore moves bytes from src into buf. It is called only when buf has no
// loadable bytes left (r == w). It first compacts buf by dropping the
// fully-consumed prefix, then reads one block.
func (r *Reader) readMore() {
	if r.err != nil {
		return
	}
	// Compact: drop fully-consumed bytes, keep the small unconsumed tail. At this
	// point every fully-consumed byte has already been tapped (the prior op ran
	// emitTaps), so fc == tapCur when a tap is installed and no un-tapped byte is
	// discarded.
	fc := r.consumedBytes()
	if fc > 0 {
		n := copy(r.buf, r.buf[fc:r.w]) // tail length is ceil(nbits/8) <= 8 bytes
		r.w = n
		r.r -= fc
		r.tapCur -= fc
		if r.tapCur < 0 {
			r.tapCur = 0
		}
	}
	// Read one block; loop until we get >0 bytes or a real end/error. Bound the
	// consecutive empty (0, nil) reads, as bufio does, so a pathological io.Reader
	// that never returns data or an error cannot spin forever.
	for empties := 0; r.w < len(r.buf); {
		n, err := r.src.Read(r.buf[r.w:])
		r.w += n
		if err != nil {
			r.err = err
			return
		}
		if n > 0 {
			return
		}
		empties++
		if empties >= maxEmptyReads {
			r.err = io.ErrNoProgress
			return
		}
	}
}

// fill tops up acc from buf as far as possible.
func (r *Reader) fill() {
	// Bulk 8-byte load when the accumulator is empty and a full word is buffered.
	if r.nbits == 0 && r.w-r.r >= 8 {
		r.acc = binary.BigEndian.Uint64(r.buf[r.r:])
		r.r += 8
		r.loaded += 8
		r.nbits = 64
		return
	}
	// Byte-at-a-time top-up (no interface dispatch, no per-byte function call).
	for r.nbits <= 56 {
		if r.r >= r.w {
			r.readMore()
			if r.r >= r.w {
				return // EOF/error: serve whatever is in acc
			}
		}
		r.acc |= uint64(r.buf[r.r]) << (56 - r.nbits) // slot just below the valid region
		r.r++
		r.loaded++
		r.nbits += 8
	}
}

// emitTaps hands buf[tapCur:fc] to the tap, in order, advancing tapCur. Callers
// gate it inline (fc > tapCur, via consumedBytes) so the common no-byte-completed
// case stays a cheap comparison and never pays a call; this keeps emitTaps small
// enough to inline and off the per-sample hot path.
func (r *Reader) emitTaps(fc int) {
	for r.tapCur < fc {
		r.tap(r.buf[r.tapCur])
		r.tapCur++
	}
}

// ReadBits reads n bits (0..64) MSB-first and returns them right-aligned.
func (r *Reader) ReadBits(n uint) (uint64, error) {
	if n <= r.nbits { // fast path; also handles n==0
		v := r.acc >> (64 - n) // n==0 -> shift by 64 -> 0
		r.acc <<= n            // n==64 -> 0; vacated low bits become 0 (keeps left-alignment)
		r.nbits -= n
		if r.tap != nil {
			if fc := r.consumedBytes(); fc > r.tapCur {
				r.emitTaps(fc)
			}
		}
		return v, nil
	}
	return r.readBitsSlow(n)
}

func (r *Reader) readBitsSlow(n uint) (uint64, error) {
	r.fill()
	if n <= r.nbits { // now serviceable
		v := r.acc >> (64 - n)
		r.acc <<= n
		r.nbits -= n
		if r.tap != nil {
			if fc := r.consumedBytes(); fc > r.tapCur {
				r.emitTaps(fc)
			}
		}
		return v, nil
	}
	// nbits < n after filling. Two distinct cases, separated by whether more data
	// exists (r.err == nil) or the stream ended (r.err != nil).
	if r.err == nil {
		// Data is still available; the accumulator simply cannot hold n more bits
		// alongside the current unaligned remainder (nbits > 64-n, so nbits is
		// 57..63 and n is 58..64). Take the whole remainder as the high part, then
		// read the rest from a freshly refilled accumulator. got + (n-got) == n and
		// each part is <= 64, so no shift-by->=64 and no second overflow.
		hi := r.acc >> (64 - r.nbits)
		got := r.nbits
		r.acc = 0
		r.nbits = 0
		if r.tap != nil {
			if fc := r.consumedBytes(); fc > r.tapCur {
				r.emitTaps(fc)
			}
		}
		lo, err := r.ReadBits(n - got)
		if err != nil {
			// The high part was already consumed, so an EOF while fetching the low
			// part is a truncated value, not a clean end of stream.
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		return hi<<(n-got) | lo, nil
	}
	// r.err != nil: genuine EOF / truncation / I/O error.
	if r.nbits == 0 {
		return 0, r.err // io.EOF (clean end at byte boundary) or the real error
	}
	if errors.Is(r.err, io.EOF) {
		return 0, io.ErrUnexpectedEOF // some bits present but fewer than n: truncated
	}
	return 0, r.err // some bits present, real I/O error
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
// returns the count of zeros. It scans zeros across the whole accumulator word via
// bits.LeadingZeros64, so a long zero run costs one iteration per 64-bit word.
func (r *Reader) ReadUnary() (uint64, error) {
	var zeros uint64
	for {
		if r.nbits == 0 {
			r.fill()
			if r.nbits == 0 {
				return 0, r.err // io.EOF or real error (frame layer translates EOF)
			}
		}
		lz := uint(bits.LeadingZeros64(r.acc)) // valid bits at top; low bits are 0
		if lz >= r.nbits {
			// Every valid bit is zero: consume them all and refill.
			zeros += uint64(r.nbits)
			r.acc = 0
			r.nbits = 0
			if r.tap != nil {
				if fc := r.consumedBytes(); fc > r.tapCur {
					r.emitTaps(fc)
				}
			}
			continue
		}
		// Terminating 1 is at position lz from the MSB, within the valid region.
		zeros += uint64(lz)
		consume := lz + 1
		r.acc <<= consume
		r.nbits -= consume
		if r.tap != nil {
			if fc := r.consumedBytes(); fc > r.tapCur {
				r.emitTaps(fc)
			}
		}
		return zeros, nil
	}
}

// SkipToByteBoundary discards remaining bits in the current partial byte so the
// next read starts on a byte boundary. The skipped byte is tapped if it had been
// partially consumed.
func (r *Reader) SkipToByteBoundary() error {
	rem := r.nbits & 7
	if rem == 0 {
		return nil // already byte-aligned: no tap
	}
	r.acc <<= rem // discard the low rem bits of the current partial byte
	r.nbits -= rem
	if r.tap != nil {
		if fc := r.consumedBytes(); fc > r.tapCur {
			r.emitTaps(fc) // the straddling byte is now fully consumed -> tapped once
		}
	}
	return nil
}

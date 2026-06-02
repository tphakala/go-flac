package bitio

// Writer packs bits MSB-first into an in-memory byte buffer. It is the inverse of
// Reader. Each frame is assembled in full, then its CRC fields are computed over
// Bytes() and appended, so no CRC tap is needed on the write side.
type Writer struct {
	buf  []byte
	acc  uint64 // pending bits, right-aligned in the low nbit bits
	nbit uint   // number of valid pending bits (0..7 between WriteBits calls)
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// WriteBits writes the low n bits of v, most-significant bit first. n must be in
// 0..57 (callers write at most 33). Bits above bit n-1 of v are ignored.
func (w *Writer) WriteBits(v uint64, n uint) {
	if n == 0 {
		return
	}
	v &= (uint64(1) << n) - 1
	w.acc = (w.acc << n) | v
	w.nbit += n
	for w.nbit >= 8 {
		w.nbit -= 8
		w.buf = append(w.buf, byte(w.acc>>w.nbit))
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
	if w.nbit > 0 {
		w.buf = append(w.buf, byte(w.acc<<(8-w.nbit)))
		w.nbit = 0
		w.acc = 0
	}
}

// Bytes returns the assembled bytes. It is only byte-exact when the writer is
// byte aligned (AlignByte called, or only whole bytes written).
func (w *Writer) Bytes() []byte { return w.buf }

// Reset clears the writer for reuse, retaining the backing array.
func (w *Writer) Reset() {
	w.buf = w.buf[:0]
	w.acc = 0
	w.nbit = 0
}

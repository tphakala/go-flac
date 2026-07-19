package bitio

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// countingReader wraps an io.Reader and records how many bytes it has delivered
// via Read, so a test can prove the bit reader read AHEAD of what it consumed.
// It deliberately does NOT implement io.ByteReader, so both the old (bufio) and
// new (owned buffer) readers buffer whole blocks from it.
type countingReader struct {
	inner io.Reader
	read  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.read += int64(n)
	return n, err
}

// TestReadBitsMidWordWide is the correction case: read a few bits to leave the
// accumulator unaligned, then request 64 bits. The 64 bits cannot fit alongside
// the unaligned remainder in a single 64-bit accumulator, so a naive slow path
// that only refills once returns a wrong value. The reader must compose the
// result across a refill and return the exact bits.
func TestReadBitsMidWordWide(t *testing.T) {
	src := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x11}
	r := NewReader(bytes.NewReader(src))
	got, err := r.ReadBits(4) // top nibble of 0x12
	if err != nil || got != 0x1 {
		t.Fatalf("ReadBits(4)=%#x err=%v, want 0x1", got, err)
	}
	// Next 64 bits: low nibble 0x2, bytes 0x34..0xF0, top nibble of 0x11 (0x1).
	// Nibble stream: 2 3 4 5 6 7 8 9 A B C D E F 0 1 => 0x23456789ABCDEF01.
	got, err = r.ReadBits(64)
	if err != nil || got != 0x23456789ABCDEF01 {
		t.Fatalf("ReadBits(64) mid-word=%#016x err=%v, want 0x23456789abcdef01", got, err)
	}
}

// TestReadSignedMidWordWide exercises the same accumulator-full split path through
// ReadSigned, which forwards to ReadBits. Read 4 bits then a signed 64-bit value.
func TestReadSignedMidWordWide(t *testing.T) {
	// After a 4-bit read, the next 64 bits are 0xF3456789ABCDEF01 (top nibble F set),
	// which as a signed 64-bit two's-complement value is negative.
	src := []byte{0x1F, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x11}
	r := NewReader(bytes.NewReader(src))
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadSigned(64)
	if err != nil {
		t.Fatal(err)
	}
	u := uint64(0xF3456789ABCDEF01)
	want := int64(u) // wrapping conversion of a variable; the top nibble F makes it negative
	if got != want {
		t.Fatalf("ReadSigned(64) mid-word=%d err=%v, want %d", got, err, want)
	}
}

// TestReadBitsFastPathBounds pins the 56/57 fast-path boundary and full width from
// a fresh (byte-aligned) accumulator.
func TestReadBitsFastPathBounds(t *testing.T) {
	src := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}
	for _, n := range []uint{55, 56, 57, 63, 64} {
		r := NewReader(bytes.NewReader(src))
		got, err := r.ReadBits(n)
		if err != nil {
			t.Fatalf("ReadBits(%d) err=%v", n, err)
		}
		full := uint64(0x123456789ABCDEF0)
		want := full >> (64 - n)
		if got != want {
			t.Fatalf("ReadBits(%d)=%#x, want %#x", n, got, want)
		}
	}
}

// TestReadBitsSecondFill reads a mixed sequence that crosses byte boundaries within
// one 64-bit word and then forces a second word refill.
func TestReadBitsSecondFill(t *testing.T) {
	// 16 bytes so the reader must fill a second word after the first is drained.
	src := make([]byte, 16)
	for i := range src {
		src[i] = byte(i*17 + 3)
	}
	r := NewReader(bytes.NewReader(src))
	// Independent bit-by-bit reference over the same bytes.
	o := newBitReadOracle(src)
	for _, n := range []uint{3, 5, 8, 20, 1, 7, 33, 4, 16, 9} {
		got, gerr := r.ReadBits(n)
		want, werr := o.readBits(n)
		if !sameErrKind(gerr, werr) || got != want {
			t.Fatalf("ReadBits(%d)=%#x err=%v, want %#x err=%v", n, got, gerr, want, werr)
		}
	}
}

// TestReadBitsZeroConsumesNothing pins ReadBits(0) == 0 with no state change.
func TestReadBitsZeroConsumesNothing(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xAB, 0xCD}))
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	beforeBytes := r.BytesRead()
	beforeAligned := r.ByteAligned()
	v, err := r.ReadBits(0)
	if err != nil || v != 0 {
		t.Fatalf("ReadBits(0)=%d err=%v, want 0", v, err)
	}
	if r.BytesRead() != beforeBytes || r.ByteAligned() != beforeAligned {
		t.Fatalf("ReadBits(0) changed state: bytes %d->%d aligned %v->%v",
			beforeBytes, r.BytesRead(), beforeAligned, r.ByteAligned())
	}
	// The following read must still land correctly.
	if v, err := r.ReadBits(4); err != nil || v != 0xB {
		t.Fatalf("ReadBits(4)=%#x err=%v, want 0xB", v, err)
	}
}

// TestReadUnaryLongRunMultiWord exercises a zero run longer than 64 bits, which the
// old byte-at-a-time scan never spanned in one accumulator word.
func TestReadUnaryLongRunMultiWord(t *testing.T) {
	// 200 zero bits == 25 bytes of 0x00, then 0x80 provides the terminating 1 at MSB.
	src := make([]byte, 25, 26)
	src = append(src, 0x80)
	r := NewReader(bytes.NewReader(src))
	q, err := r.ReadUnary()
	if err != nil || q != 200 {
		t.Fatalf("ReadUnary long run=%d err=%v, want 200", q, err)
	}
}

// TestReadUnaryTerminatingOneAtMSB pins the zero-count-0 case.
func TestReadUnaryTerminatingOneAtMSB(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0x80}))
	q, err := r.ReadUnary()
	if err != nil || q != 0 {
		t.Fatalf("ReadUnary MSB-one=%d err=%v, want 0", q, err)
	}
}

// TestReadUnaryRunEndsAtWordBoundary makes the zero run end exactly on a 64-bit word
// boundary, so the terminating 1 is the first bit of the next refilled word.
func TestReadUnaryRunEndsAtWordBoundary(t *testing.T) {
	// 64 zero bits == 8 bytes of 0x00, then 0x80 => terminating 1 in the next word.
	src := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0x80}
	r := NewReader(bytes.NewReader(src))
	q, err := r.ReadUnary()
	if err != nil || q != 64 {
		t.Fatalf("ReadUnary word-boundary run=%d err=%v, want 64", q, err)
	}
}

// TestReadUnaryThenBitsAcrossRefill is the Rice shape: a unary quotient followed by
// a fixed-width remainder, straddling an accumulator refill.
func TestReadUnaryThenBitsAcrossRefill(t *testing.T) {
	// Build: 70 zeros, a 1, then a 12-bit value 0xABC. Verify against the oracle.
	var w Writer
	w.WriteUnary(70)
	w.WriteBits(0xABC, 12)
	w.AlignByte()
	src := w.Bytes()
	r := NewReader(bytes.NewReader(src))
	if q, err := r.ReadUnary(); err != nil || q != 70 {
		t.Fatalf("ReadUnary=%d err=%v, want 70", q, err)
	}
	if v, err := r.ReadBits(12); err != nil || v != 0xABC {
		t.Fatalf("ReadBits(12)=%#x err=%v, want 0xABC", v, err)
	}
}

// TestTapRecordsExactConsumedBytesMixed drives a mixed op stream with a recording tap
// and compares the tap log against the bytes whose LAST bit was consumed, computed
// independently from a running consumed-bit count. This is the direct bitio-level
// proof that the tap fires exactly once per fully-consumed byte, in order.
func TestTapRecordsExactConsumedBytesMixed(t *testing.T) {
	// Bytes 1..3 are zero so a ReadUnary spans three whole bytes plus part of byte 4,
	// exercising multi-byte tapping inside one unary. Byte 4 (0x08) supplies the
	// terminating 1 after four more zeros.
	src := []byte{0xB1, 0x00, 0x00, 0x00, 0x08, 0xFF, 0x0F, 0xA5, 0x5A, 0x3C, 0xD2, 0x81}
	var tapped []byte
	r := NewReader(bytes.NewReader(src))
	r.SetTap(func(b byte) { tapped = append(tapped, b) })

	consumed := 0 // bits consumed so far
	expectFullyConsumed := func() []byte {
		return src[:consumed/8]
	}
	// A scripted sequence of reads that crosses bytes and includes a skip of a
	// partially-consumed byte.
	if _, err := r.ReadBits(3); err != nil { // consume 3 bits of byte 0
		t.Fatal(err)
	}
	consumed += 3
	if _, err := r.ReadBits(5); err != nil { // finish byte 0
		t.Fatal(err)
	}
	consumed += 5
	if !bytes.Equal(tapped, expectFullyConsumed()) {
		t.Fatalf("after 8 bits tap=%x want %x", tapped, expectFullyConsumed())
	}
	// From bit 8: bytes 1,2,3 are 24 zeros, then byte 4 (0x08=0000_1000) gives four
	// more zeros then a 1. zeros = 28, consuming 29 bits (24 + 5). Bytes 1,2,3 become
	// fully consumed; byte 4 is only partially consumed, so it is not tapped yet.
	if _, err := r.ReadUnary(); err != nil {
		t.Fatal(err)
	}
	consumed += 29
	if !bytes.Equal(tapped, expectFullyConsumed()) {
		t.Fatalf("after unary tap=%x want %x", tapped, expectFullyConsumed())
	}
	if _, err := r.ReadBits(4); err != nil { // finishes byte 4, dips into byte 5
		t.Fatal(err)
	}
	consumed += 4
	if err := r.SkipToByteBoundary(); err != nil { // taps the partially-consumed byte 5
		t.Fatal(err)
	}
	consumed += (8 - consumed%8) % 8
	if !bytes.Equal(tapped, expectFullyConsumed()) {
		t.Fatalf("after skip tap=%x want %x", tapped, expectFullyConsumed())
	}
	if _, err := r.ReadBits(24); err != nil { // bytes 6,7,8
		t.Fatal(err)
	}
	consumed += 24
	if !bytes.Equal(tapped, expectFullyConsumed()) {
		t.Fatalf("after 24 bits tap=%x want %x", tapped, expectFullyConsumed())
	}
}

// TestEOFCleanAtByteBoundary: a read that starts exactly at end of stream returns
// io.EOF (a clean end), never io.ErrUnexpectedEOF.
func TestEOFCleanAtByteBoundary(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xAB}))
	if _, err := r.ReadBits(8); err != nil { // consume the only byte
		t.Fatal(err)
	}
	if _, err := r.ReadBits(1); !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("clean-end ReadBits(1) err=%v, want io.EOF", err)
	}
}

// TestEOFTruncationMidValue: fewer bits remain than requested => io.ErrUnexpectedEOF.
func TestEOFTruncationMidValue(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xAB})) // 8 bits available
	if _, err := r.ReadBits(14); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated ReadBits(14) err=%v, want io.ErrUnexpectedEOF", err)
	}
}

// TestReadUnaryEOFMidRunIsRawEOF: ReadUnary running off the end returns io.EOF raw
// (not translated to ErrUnexpectedEOF); the frame layer performs that translation.
func TestReadUnaryEOFMidRunIsRawEOF(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0x00, 0x00})) // all zeros, no terminating 1
	_, err := r.ReadUnary()
	if !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadUnary off-end err=%v, want raw io.EOF", err)
	}
}

// TestBytesReadIsConsumedNotRead proves BytesRead reports CONSUMED bytes even when the
// reader has read a whole block ahead from the source.
func TestBytesReadIsConsumedNotRead(t *testing.T) {
	buf := make([]byte, 100)
	for i := range buf {
		buf[i] = byte(i)
	}
	cr := &countingReader{inner: bytes.NewReader(buf)}
	r := NewReader(cr)
	if _, err := r.ReadBits(24); err != nil { // consume exactly 3 bytes
		t.Fatal(err)
	}
	if got := r.BytesRead(); got != 3 {
		t.Fatalf("BytesRead=%d, want 3 (consumed, not read)", got)
	}
	if cr.read <= 3 {
		t.Fatalf("expected read-ahead beyond 3 bytes, source delivered only %d", cr.read)
	}
	if !r.ByteAligned() {
		t.Fatal("expected byte aligned after 24 bits")
	}
}

// TestByteAlignedWithBufferedWholeBytes: with several whole bytes buffered in the
// accumulator, alignment tracks nbits&7, not nbits==0.
func TestByteAlignedWithBufferedWholeBytes(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}))
	if _, err := r.ReadBits(8); err != nil { // one whole byte; more remain buffered
		t.Fatal(err)
	}
	if !r.ByteAligned() {
		t.Fatal("aligned after 8 bits even with whole bytes buffered")
	}
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if r.ByteAligned() {
		t.Fatal("not aligned after 12 bits")
	}
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if !r.ByteAligned() {
		t.Fatal("aligned again after 16 bits")
	}
}

// TestNewReaderAtSeekAndRead confirms basePos is added to the consumed count so an
// absolute byte position after a seek stays correct.
func TestNewReaderAtSeekAndRead(t *testing.T) {
	r := NewReaderAt(bytes.NewReader([]byte{0x55, 0x66, 0x77, 0x88}), 100)
	if _, err := r.ReadBits(16); err != nil { // consume two bytes
		t.Fatal(err)
	}
	if got := r.BytesRead(); got != 102 {
		t.Fatalf("BytesRead=%d, want 102 (100 base + 2 consumed)", got)
	}
}

// TestReadBitsSplitReadWithTap exercises the SPLIT-READ path in readBitsSlow with a
// tap installed, which no existing test does. Reading 4 bits first leaves the
// accumulator holding 60 unaligned bits (nbits in 57..63), so the following
// ReadBits(64) cannot be served from one fill: it takes the remainder as a high
// part, emits taps for the bytes that high part fully consumes, then recurses for
// the low part. Both the returned value (checked against an independent bit-by-bit
// oracle) and the tapped byte sequence are asserted.
func TestReadBitsSplitReadWithTap(t *testing.T) {
	src := []byte{0xA5, 0x3C, 0x7E, 0x91, 0xFD, 0x08, 0x66, 0x2B, 0xC4}
	var tapped []byte
	r := NewReader(bytes.NewReader(src))
	r.SetTap(func(b byte) { tapped = append(tapped, b) })
	o := newBitReadOracle(src)

	got4, err := r.ReadBits(4) // unaligns the accumulator (nbits becomes 60)
	if err != nil {
		t.Fatalf("ReadBits(4) err=%v", err)
	}
	want4, werr := o.readBits(4)
	if werr != nil || got4 != want4 {
		t.Fatalf("ReadBits(4)=%#x err=%v, want %#x (oracle err=%v)", got4, err, want4, werr)
	}

	got64, err := r.ReadBits(64) // forces the split-read path
	if err != nil {
		t.Fatalf("ReadBits(64) err=%v", err)
	}
	want64, werr := o.readBits(64)
	if werr != nil || got64 != want64 {
		t.Fatalf("ReadBits(64)=%#016x err=%v, want %#016x (oracle err=%v)", got64, err, want64, werr)
	}

	// 4 + 64 = 68 bits consumed so far, which is 8 whole bytes plus 4 leftover bits:
	// only the first 8 source bytes have had their last bit consumed.
	wantTapped := src[:8]
	if !bytes.Equal(tapped, wantTapped) {
		t.Fatalf("tap=%x, want %x", tapped, wantTapped)
	}
}

// TestReadBitsSplitReadTruncation forces the split-read path's low-part fetch to run
// off the end of the source. The high part has already been consumed by the time the
// low part hits EOF, so the value is truncated mid-read and the error must be
// io.ErrUnexpectedEOF, never a raw io.EOF.
func TestReadBitsSplitReadTruncation(t *testing.T) {
	// Exactly 8 bytes (64 bits): after ReadBits(4) leaves 60 bits buffered, the
	// following ReadBits(64) needs 4 more bits than the source has left.
	src := []byte{0x9F, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	r := NewReader(bytes.NewReader(src))
	if _, err := r.ReadBits(4); err != nil {
		t.Fatalf("ReadBits(4) err=%v", err)
	}
	_, err := r.ReadBits(64)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadBits(64) err=%v, want io.ErrUnexpectedEOF", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("ReadBits(64) err=%v reported as a clean io.EOF; the high part was already consumed, so this must be a truncation", err)
	}
}

// errBoom is a sentinel I/O error distinct from any EOF variant, used to prove a real
// error from the source is propagated raw.
var errBoom = errors.New("boom")

// errAfterDataReader delivers data once, then fails every subsequent Read with
// errBoom, modeling a source whose underlying I/O breaks partway through a stream.
type errAfterDataReader struct {
	data []byte
	sent bool
}

func (e *errAfterDataReader) Read(p []byte) (int, error) {
	if !e.sent {
		e.sent = true
		return copy(p, e.data), nil
	}
	return 0, errBoom
}

// TestReaderNonEOFErrorPropagates proves a non-EOF I/O error from the source comes
// back from ReadBits unchanged, not remapped to io.EOF or io.ErrUnexpectedEOF the way
// a genuine end-of-stream is.
func TestReaderNonEOFErrorPropagates(t *testing.T) {
	src := &errAfterDataReader{data: []byte{0x12, 0x34}}
	r := NewReader(src)
	if _, err := r.ReadBits(16); err != nil { // consumes exactly the delivered bytes
		t.Fatalf("ReadBits(16) err=%v", err)
	}
	if _, err := r.ReadBits(8); !errors.Is(err, errBoom) {
		t.Fatalf("ReadBits(8) err=%v, want errBoom", err)
	}
}

// zeroReader always reports success with zero bytes delivered, the pathological
// (0, nil) source the maxEmptyReads guard exists to bound.
type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

// TestReaderNoProgressGuard proves readMore cannot spin forever against a source that
// never delivers data or an error: it must give up after maxEmptyReads attempts and
// return io.ErrNoProgress. The read runs on a goroutine with a generous timeout so a
// regression that removes the guard fails the test instead of hanging the suite.
func TestReaderNoProgressGuard(t *testing.T) {
	r := NewReader(zeroReader{})
	done := make(chan error, 1)
	go func() {
		_, err := r.ReadBits(8)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrNoProgress) {
			t.Fatalf("ReadBits(8) err=%v, want io.ErrNoProgress", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadBits(8) did not return within 5s: readMore appears to spin forever on a no-progress source")
	}
}

// TestReadSignedZero pins ReadSigned(0) == 0 with no consumption, mirroring
// TestReadBitsZeroConsumesNothing at the ReadSigned entry point.
func TestReadSignedZero(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0xAB, 0xCD}))
	beforeBytes := r.BytesRead()
	v, err := r.ReadSigned(0)
	if err != nil || v != 0 {
		t.Fatalf("ReadSigned(0)=%d err=%v, want 0", v, err)
	}
	if r.BytesRead() != beforeBytes {
		t.Fatalf("ReadSigned(0) consumed bytes: before=%d after=%d", beforeBytes, r.BytesRead())
	}
	// A following read must still land correctly, proving nothing was consumed.
	if got, err := r.ReadBits(8); err != nil || got != 0xAB {
		t.Fatalf("ReadBits(8)=%#x err=%v, want 0xab", got, err)
	}
}

// TestSetTapUnalignedReseat installs a tap while the cursor sits mid-byte (nbits&7 !=
// 0), then reads across a byte boundary, and asserts the tap fires only for bytes
// fully consumed AFTER SetTap was called, never for bits consumed before it.
func TestSetTapUnalignedReseat(t *testing.T) {
	src := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	r := NewReader(bytes.NewReader(src))
	if _, err := r.ReadBits(3); err != nil { // unaligns the cursor
		t.Fatal(err)
	}
	if r.ByteAligned() {
		t.Fatal("expected an unaligned cursor before SetTap")
	}

	var tapped []byte
	r.SetTap(func(b byte) { tapped = append(tapped, b) })

	if _, err := r.ReadBits(8); err != nil { // finishes byte 0
		t.Fatal(err)
	}
	if _, err := r.ReadBits(16); err != nil { // consumes bytes 1 and 2 whole
		t.Fatal(err)
	}

	want := src[:3] // fully consumed only after SetTap was installed
	if !bytes.Equal(tapped, want) {
		t.Fatalf("tap=%x, want %x (bits consumed before SetTap must not appear)", tapped, want)
	}
}

// TestReaderResetReusesBuffer proves Reset (a) produces reads correct and
// independent of the prior source's state, (b) resets BytesRead to 0, and (c)
// reuses the owned 8 KiB buffer's backing array instead of reallocating it, which
// the frame resync scan (many short candidate sources) relies on.
func TestReaderResetReusesBuffer(t *testing.T) {
	srcA := make([]byte, readBlock)
	for i := range srcA {
		srcA[i] = 0xAA
	}
	srcB := []byte{0x12, 0x34, 0x56, 0x78}

	r := NewReader(bytes.NewReader(srcA))
	if _, err := r.ReadBits(32); err != nil { // pulls a whole block into buf/acc
		t.Fatal(err)
	}
	if got := r.BytesRead(); got != 4 {
		t.Fatalf("BytesRead before Reset=%d, want 4", got)
	}

	r.Reset(bytes.NewReader(srcB))
	if got := r.BytesRead(); got != 0 {
		t.Fatalf("BytesRead after Reset=%d, want 0", got)
	}
	got, err := r.ReadBits(32)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(0x12345678); got != want {
		t.Fatalf("ReadBits(32) after Reset=%#x, want %#x (must be independent of source A)", got, want)
	}
	if got := r.BytesRead(); got != 4 {
		t.Fatalf("BytesRead after read=%d, want 4", got)
	}

	// Warm up once so any one-time setup cost is outside the measured loop, then
	// measure a Reset+small-read cycle. A regression that reallocates the owned
	// buffer inside Reset adds one extra allocation per cycle on top of the
	// bytes.Reader that bytes.NewReader allocates each iteration, so the count-based
	// threshold below distinguishes reuse (1) from reallocation (2+).
	r.Reset(bytes.NewReader(srcB))
	if _, err := r.ReadBits(8); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(20, func() {
		r.Reset(bytes.NewReader(srcB))
		if _, err := r.ReadBits(8); err != nil {
			t.Errorf("ReadBits(8) in AllocsPerRun err=%v", err)
		}
	})
	if allocs > 1 {
		t.Fatalf("Reset+read allocated %.1f objects/op, want <= 1 (owned buffer must be reused, not reallocated)", allocs)
	}
}

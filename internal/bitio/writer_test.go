package bitio

import (
	"bytes"
	"testing"
)

func TestWriterPacksMSBFirst(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0b101, 3)
	w.WriteBits(0x14E, 9) // 1_0100_1110
	w.AlignByte()
	// stream MSB-first: 101 101001110 -> "10110100" "1110 0000" = 0xB4 0xE0
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xB4, 0xE0}) {
		t.Fatalf("Bytes=% x, want B4 E0", got)
	}
}

func TestWriterReaderRoundTrip(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0x3FFE, 14)
	w.WriteSignedBits(-3, 4)
	w.WriteSignedBits(7, 5)
	w.WriteUnary(0)
	w.WriteUnary(5)
	w.WriteBits(0b1011, 4)
	w.AlignByte()

	r := NewReader(bytes.NewReader(w.Bytes()))
	if v, err := r.ReadBits(14); err != nil || v != 0x3FFE {
		t.Fatalf("ReadBits(14)=%#x err=%v", v, err)
	}
	if v, err := r.ReadSigned(4); err != nil || v != -3 {
		t.Fatalf("ReadSigned(4)=%d err=%v", v, err)
	}
	if v, err := r.ReadSigned(5); err != nil || v != 7 {
		t.Fatalf("ReadSigned(5)=%d err=%v", v, err)
	}
	if v, err := r.ReadUnary(); err != nil || v != 0 {
		t.Fatalf("ReadUnary()=%d err=%v", v, err)
	}
	if v, err := r.ReadUnary(); err != nil || v != 5 {
		t.Fatalf("ReadUnary()=%d err=%v", v, err)
	}
	if v, err := r.ReadBits(4); err != nil || v != 0b1011 {
		t.Fatalf("ReadBits(4)=%#b err=%v", v, err)
	}
}

// TestWriterGrowDoesNotAlterOutput checks that Grow is purely a capacity hint by
// encoding the same non-trivial, multi-word bit sequence twice, once with a
// preceding Grow and once without, and asserting the two byte streams are byte
// identical. The sequence spans many 64-bit words so it exercises flushWord under
// both reserved and unreserved capacity (a true A/B comparison, not a check against
// a single hand-derived literal).
func TestWriterGrowDoesNotAlterOutput(t *testing.T) {
	writeSeq := func(w *Writer) {
		for i := range 60 {
			w.WriteBits(uint64(i)*2654435761, 17) // 17-bit values straddle word boundaries
			w.WriteUnary(uint64(i % 7))
		}
		w.AlignByte()
	}
	var withGrow, withoutGrow Writer
	withGrow.Grow(1024)
	writeSeq(&withGrow)
	writeSeq(&withoutGrow)
	if !bytes.Equal(withGrow.Bytes(), withoutGrow.Bytes()) {
		t.Fatalf("Grow altered output:\n withGrow = % x\n noGrow   = % x", withGrow.Bytes(), withoutGrow.Bytes())
	}
	// Also pin the exact bytes against a hand-derived baseline so a bug corrupting
	// BOTH paths identically is still caught.
	w := NewWriter()
	w.Grow(64)
	w.WriteBits(0b101, 3)
	w.WriteBits(0x14E, 9) // 1_0100_1110
	w.AlignByte()
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xB4, 0xE0}) {
		t.Fatalf("Bytes=% x, want B4 E0 (Grow must not alter emitted bytes)", got)
	}
}

// TestWriterGrowReservesCapacityWithoutChangingLen checks that Grow does not
// change len(Bytes()) (a fresh Writer stays empty after Grow) and that writes
// after Grow still land correctly.
func TestWriterGrowReservesCapacityWithoutChangingLen(t *testing.T) {
	w := NewWriter()
	if got := len(w.Bytes()); got != 0 {
		t.Fatalf("fresh writer Bytes len=%d, want 0", got)
	}
	w.Grow(128)
	if got := len(w.Bytes()); got != 0 {
		t.Fatalf("after Grow, Bytes len=%d, want 0 (Grow must not change len)", got)
	}
	if cap(w.buf) < 128 {
		t.Fatalf("after Grow(128), cap(buf)=%d, want >= 128", cap(w.buf))
	}
	w.WriteBits(0xAB, 8)
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xAB}) {
		t.Fatalf("after Grow+write, Bytes=% x, want AB", got)
	}
}

// TestWriterGrowNonPositiveIsNoop checks that Grow(0) and a negative n do not
// panic and do not change the writer's observable state.
func TestWriterGrowNonPositiveIsNoop(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0x7, 3)
	w.Grow(0)
	w.Grow(-5)
	w.AlignByte()
	if got := w.Bytes(); !bytes.Equal(got, []byte{0xE0}) {
		t.Fatalf("Bytes=% x, want E0", got)
	}
}

// TestWriterGrowPreservesExistingContent pins Grow's documented guarantee that it
// never truncates or mutates buf's existing contents. Bytes already flushed into
// buf before a Grow call must survive unchanged. This kills a mutation such as
// slices.Grow(w.buf[:0], n), which grows a zero-length reslice and silently drops
// everything already written; that mutation passes every other test here because
// they only ever Grow a still-empty buffer.
func TestWriterGrowPreservesExistingContent(t *testing.T) {
	w := NewWriter()
	want := make([]byte, 0, 20)
	// Write 10 whole bytes first: 8 of them force a flushWord into buf, so buf is
	// non-empty when Grow is called.
	for i := range 10 {
		w.WriteBits(uint64(0xA0+i), 8)
		want = append(want, byte(0xA0+i))
	}
	w.Grow(4096) // grow with data already in buf
	for i := 10; i < 20; i++ {
		w.WriteBits(uint64(0xA0+i), 8)
		want = append(want, byte(0xA0+i))
	}
	w.AlignByte()
	if got := w.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("Bytes=% x, want % x (Grow must preserve existing buffered bytes)", got, want)
	}
}

// TestWriterGrowAvoidsReallocation checks that Grow actually reserves usable
// capacity: after Grow(n) with n covering the whole write, writing that data must
// not reallocate buf's backing array (cap stays constant). This is the capacity
// reuse EncodeFrame relies on to keep flushWord off the allocation path.
func TestWriterGrowAvoidsReallocation(t *testing.T) {
	const nbytes = 4096
	w := NewWriter()
	w.Grow(nbytes)
	capAfterGrow := cap(w.buf)
	for range nbytes {
		w.WriteBits(0xFF, 8) // 4096 bytes == 512 full flushWord words
	}
	w.AlignByte()
	if cap(w.buf) != capAfterGrow {
		t.Fatalf("buf reallocated during writes: cap %d -> %d (Grow did not reserve enough)", capAfterGrow, cap(w.buf))
	}
	if got := len(w.Bytes()); got != nbytes {
		t.Fatalf("wrote %d bytes, want %d", got, nbytes)
	}
}

func TestWriterResetClears(t *testing.T) {
	w := NewWriter()
	w.WriteBits(0xFF, 8)
	w.Reset()
	if len(w.Bytes()) != 0 {
		t.Fatalf("after Reset, Bytes len=%d, want 0", len(w.Bytes()))
	}
	w.WriteBits(0x01, 8)
	if got := w.Bytes(); !bytes.Equal(got, []byte{0x01}) {
		t.Fatalf("after Reset+write, Bytes=% x, want 01", got)
	}
}

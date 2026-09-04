package bitio

import (
	"bytes"
	"testing"
)

// rangeRecorder implements both ByteTap and ByteRangeTap and records which path fired,
// so a test can assert the deferred bulk path is taken (and the per-byte path is not).
type rangeRecorder struct {
	rec        []byte
	byteCalls  int
	rangeCalls int
}

func (r *rangeRecorder) TapByte(b byte)    { r.byteCalls++; r.rec = append(r.rec, b) }
func (r *rangeRecorder) TapBytes(p []byte) { r.rangeCalls++; r.rec = append(r.rec, p...) }

// TestDeferredRangeTapRecordsAllBytesAcrossRefills drives the deferred bulk tap over a
// source several reader blocks long, so it exercises the record path across multiple
// readMore refills (where each consumed run must be flushed before the buffer is
// compacted and its bytes dropped) plus the final FlushTap sweep. It asserts every
// consumed byte is recorded in order, that the per-byte TapByte path never fires once
// deferred (the hot decode path must do no per-byte tap work), and that several bulk
// runs were flushed (proving refills actually happened).
func TestDeferredRangeTapRecordsAllBytesAcrossRefills(t *testing.T) {
	const n = readBlock*3 + 123 // spans three-plus refills and ends mid-block
	src := make([]byte, n)
	for i := range src {
		src[i] = byte(i*31 + 7)
	}

	r := NewReader(bytes.NewReader(src))
	var rec rangeRecorder
	r.SetTapper(&rec)
	r.SwitchTapToDeferred()
	for range n {
		if _, err := r.ReadBits(8); err != nil {
			t.Fatalf("ReadBits(8): %v", err)
		}
	}
	r.FlushTap()

	if !bytes.Equal(rec.rec, src) {
		t.Fatalf("recorded %d bytes != source %d bytes: a deferred run was dropped across a refill", len(rec.rec), n)
	}
	if rec.byteCalls != 0 {
		t.Fatalf("TapByte fired %d times in deferred mode; the hot path must do no per-byte tap work", rec.byteCalls)
	}
	if rec.rangeCalls < 2 {
		t.Fatalf("TapBytes fired %d times; a source spanning several refills must flush multiple bulk runs", rec.rangeCalls)
	}
}

// TestSwitchTapToDeferredSpliceMatchesPerByte proves the header-to-body handoff records
// a seamless byte stream: the same source is recorded once fully per-byte and once split
// (a per-byte prefix, then SwitchTapToDeferred, then a deferred bulk remainder). Both
// recordings must be byte-identical, with no byte skipped or duplicated at the switch.
func TestSwitchTapToDeferredSpliceMatchesPerByte(t *testing.T) {
	const n = readBlock + 500 // long enough that the deferred remainder crosses a refill
	src := make([]byte, n)
	for i := range src {
		src[i] = byte(i*131 + 17)
	}

	// Reference: record everything through the per-byte path.
	rPB := NewReader(bytes.NewReader(src))
	var pb rangeRecorder
	rPB.SetTapper(&pb)
	for range n {
		if _, err := rPB.ReadBits(8); err != nil {
			t.Fatalf("per-byte ReadBits(8): %v", err)
		}
	}
	if !bytes.Equal(pb.rec, src) {
		t.Fatalf("per-byte recording %d bytes != source %d", len(pb.rec), n)
	}

	// Split: 40 bytes per-byte, then switch to deferred for the rest (crossing a refill).
	rSp := NewReader(bytes.NewReader(src))
	var sp rangeRecorder
	rSp.SetTapper(&sp)
	const prefix = 40
	for range prefix {
		if _, err := rSp.ReadBits(8); err != nil {
			t.Fatalf("prefix ReadBits(8): %v", err)
		}
	}
	rSp.SwitchTapToDeferred()
	for range n - prefix {
		if _, err := rSp.ReadBits(8); err != nil {
			t.Fatalf("deferred ReadBits(8): %v", err)
		}
	}
	rSp.FlushTap()

	if !bytes.Equal(sp.rec, src) {
		t.Fatalf("split recording %d bytes != source %d: header/body handoff skipped or duplicated a byte", len(sp.rec), n)
	}
	if sp.byteCalls != prefix {
		t.Fatalf("TapByte fired %d times, want %d (only the per-byte prefix)", sp.byteCalls, prefix)
	}
}

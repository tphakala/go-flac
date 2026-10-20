package pcm

import (
	"bytes"
	"io"
	"runtime"
	"testing"
)

// readCountingSeeker counts Read calls on a seekable in-memory source, so a test
// can see how many requests the decoder's buffered reader issues. Seeks are
// forwarded untouched and are not counted.
type readCountingSeeker struct {
	*bytes.Reader
	reads int
}

func (c *readCountingSeeker) Read(p []byte) (int, error) {
	c.reads++
	return c.Reader.Read(p)
}

// TestDecoderReadsSourceInLargeBlocks pins the decoder's source read size. The
// decoder reads the caller's source directly (no bufio), so each refill is one
// Read call, which is one syscall on an *os.File. Each row covers one way the
// decoder's reader is built: a fresh decoder, a Reset onto a decoder that already
// owns a buffer, and the reader rebuilt after a seek. Decoding to the end also
// verifies the STREAMINFO MD5 on the first two rows, so frames that straddle 64
// KiB refills are checked end to end.
func TestDecoderReadsSourceInLargeBlocks(t *testing.T) {
	const block = 64 << 10 // hard-coded: the contract is a 64 KiB request, not whatever the constant says
	stream := makeStream(t, resetShapes["stereo16"], 100000)
	maxReads := len(stream)/block + 2 // one request per block, plus the EOF read

	drain := func(t *testing.T, d *Decoder) {
		t.Helper()
		if _, err := io.Copy(io.Discard, d); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}

	t.Run("fresh", func(t *testing.T) {
		src := &readCountingSeeker{Reader: bytes.NewReader(stream)}
		d, err := NewDecoder(src)
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		drain(t, d)
		if src.reads > maxReads {
			t.Errorf("fresh decoder issued %d reads for %d bytes, want <= %d", src.reads, len(stream), maxReads)
		}
	})

	t.Run("reset_keeps_block", func(t *testing.T) {
		other := makeStream(t, resetShapes["mono24"], 9000)
		d, err := NewDecoder(bytes.NewReader(other))
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		drain(t, d)
		src := &readCountingSeeker{Reader: bytes.NewReader(stream)}
		if err := d.Reset(src); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		drain(t, d)
		if src.reads > maxReads {
			t.Errorf("reset decoder issued %d reads for %d bytes, want <= %d", src.reads, len(stream), maxReads)
		}
	})

	t.Run("after_seek", func(t *testing.T) {
		src := &readCountingSeeker{Reader: bytes.NewReader(stream)}
		d, err := NewDecoder(src)
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		if _, err := d.SeekToSample(50000); err != nil {
			t.Fatalf("SeekToSample: %v", err)
		}
		src.reads = 0 // count only the reads the rebuilt reader issues
		drain(t, d)
		if src.reads > maxReads {
			t.Errorf("post-seek decode issued %d reads, want <= %d", src.reads, maxReads)
		}
	})
}

// TestDecoderReusesReadBuffer measures bytes allocated, not objects, because the
// read buffer is a single large object that an allocation count barely notices
// (see TestDecodeResetReuseAllocs). Both a Reset and a seek must reuse the
// decoder's read buffer; a path that built a fresh one would allocate at least the
// 8 KiB bitio default, far above the bound. TotalAlloc is cumulative, so GC timing
// does not matter. Not parallel: a concurrent test's allocations would be counted.
func TestDecoderReusesReadBuffer(t *testing.T) {
	const perOpLimit = 4096 // bytes; measured per-op churn is a few hundred bytes
	const iters = 20
	stream := makeStream(t, resetShapes["stereo16"], 100000)
	targets := []int64{10000, 60000, 30000, 90000}

	d, err := NewDecoder(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if _, err := io.Copy(io.Discard, d); err != nil {
		t.Fatalf("warm decode: %v", err)
	}
	for _, s := range targets { // warm the probe buffer and the seek path
		if _, err := d.SeekToSample(s); err != nil {
			t.Fatalf("warm seek: %v", err)
		}
	}
	sr := bytes.NewReader(stream)

	perOp := func(op func(i int)) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := range iters {
			op(i)
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / iters
	}

	resetBytes := perOp(func(int) {
		sr.Reset(stream)
		if err := d.Reset(sr); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		if _, err := io.Copy(io.Discard, d); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
	t.Logf("Reset+decode allocates %d B/op", resetBytes)
	if resetBytes >= perOpLimit {
		t.Errorf("Reset+decode allocated %d B/op, want < %d (read buffer not reused)", resetBytes, perOpLimit)
	}

	seekBytes := perOp(func(i int) {
		if _, err := d.SeekToSample(targets[i%len(targets)]); err != nil {
			t.Fatalf("SeekToSample: %v", err)
		}
	})
	t.Logf("SeekToSample allocates %d B/op", seekBytes)
	if seekBytes >= perOpLimit {
		t.Errorf("SeekToSample allocated %d B/op, want < %d (read buffer not reused)", seekBytes, perOpLimit)
	}
}

package pcm

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// errSourceIO is the sentinel a failingSource returns once armed.
var errSourceIO = errors.New("simulated source I/O failure")

// failingSource is an io.ReadSeeker over a fixed buffer whose Read starts returning
// errSourceIO once armed, after allowing a set number of further reads. It models a
// source (a network-backed file, a failing disk) that errors part-way through a seek,
// which an in-memory or local-file source practically never does.
type failingSource struct {
	r     *bytes.Reader
	armed bool
	allow int // reads still permitted before the failure, once armed
}

func newFailingSource(data []byte) *failingSource {
	return &failingSource{r: bytes.NewReader(data)}
}

// arm makes the (allow+1)-th subsequent Read fail.
func (f *failingSource) arm(allow int) {
	f.armed = true
	f.allow = allow
}

func (f *failingSource) disarm() { f.armed = false }

func (f *failingSource) Read(p []byte) (int, error) {
	if f.armed {
		if f.allow <= 0 {
			return 0, errSourceIO
		}
		f.allow--
	}
	return f.r.Read(p)
}

func (f *failingSource) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }

// TestSeekIOErrorInvalidatesDecoder pins the contract that a seek which fails on a
// genuine source I/O error leaves the decoder hard-failed. The seek repositions the
// shared source cursor, so the live bit reader's buffered bytes and logical position
// no longer correspond to the source. A caller that treats the seek error as
// non-fatal and reads on must get an error, never silently wrong audio.
func TestSeekIOErrorInvalidatesDecoder(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 8*4096)
	src := newFailingSource(data)
	dec, err := NewDecoder(src)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// Decode a little first, so the bit reader holds a buffer and a live position
	// that the failed seek will invalidate.
	if _, err := io.ReadFull(dec, make([]byte, 64)); err != nil {
		t.Fatalf("initial read: %v", err)
	}

	src.arm(0) // the seek's very first source read fails
	if _, err := dec.SeekToSample(6 * 4096); !errors.Is(err, errSourceIO) {
		t.Fatalf("SeekToSample err = %v, want errSourceIO", err)
	}

	// Let the source succeed again: a decoder that merely forwards live I/O errors
	// would now happily return bytes decoded from the wrong offset. Only a decoder
	// that invalidated itself still reports the failure.
	src.disarm()
	if n, err := dec.Read(make([]byte, 64)); !errors.Is(err, errSourceIO) {
		t.Fatalf("Read after a failed seek = (%d, %v), want 0 and errSourceIO", n, err)
	}
	if n, err := dec.WriteTo(io.Discard); !errors.Is(err, errSourceIO) {
		t.Fatalf("WriteTo after a failed seek = (%d, %v), want 0 and errSourceIO", n, err)
	}
}

// TestFailedSeekNeverSilentlyResumes sweeps the point at which the source starts
// failing, so every internal stage of a seek is covered (the nominal-block discovery
// probe, the binary-search and linear-scan probes, and the landing decode) without
// depending on how many reads each stage happens to take. The invariant at every
// point: either the seek succeeds, or it fails and the decoder refuses to serve
// audio afterwards.
func TestFailedSeekNeverSilentlyResumes(t *testing.T) {
	data, _ := encodeRamp(t, 2, 16, 8*4096)
	for allow := range 12 {
		src := newFailingSource(data)
		dec, err := NewDecoder(src)
		if err != nil {
			t.Fatalf("allow=%d: NewDecoder: %v", allow, err)
		}
		if _, err := io.ReadFull(dec, make([]byte, 64)); err != nil {
			t.Fatalf("allow=%d: initial read: %v", allow, err)
		}

		src.arm(allow)
		_, seekErr := dec.SeekToSample(6 * 4096)
		src.disarm()
		if seekErr == nil {
			continue // the seek completed before the failure point; nothing to assert
		}

		if n, err := dec.Read(make([]byte, 64)); err == nil {
			t.Errorf("allow=%d: Read after seek error %v returned %d bytes and no error; "+
				"a failed seek must not leave the decoder silently readable", allow, seekErr, n)
		}
	}
}

// TestSeekToEndAfterFailedSeek covers the one recovery path that clears the sticky
// error without rebuilding the bit reader: seeking at or past TotalSamples positions
// at end-of-stream directly. The next read must report a clean io.EOF rather than
// touching the reader the failed seek dropped.
func TestSeekToEndAfterFailedSeek(t *testing.T) {
	data, total := encodeRamp(t, 2, 16, 8*4096)
	src := newFailingSource(data)
	dec, err := NewDecoder(src)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	src.arm(0)
	if _, err := dec.SeekToSample(6 * 4096); !errors.Is(err, errSourceIO) {
		t.Fatalf("SeekToSample err = %v, want errSourceIO", err)
	}
	src.disarm()

	landed, err := dec.SeekToSample(int64(total))
	if err != nil {
		t.Fatalf("SeekToSample to end after a failed seek: %v", err)
	}
	if landed != int64(total) {
		t.Errorf("landed at %d, want the total sample count %d", landed, total)
	}
	if n, err := dec.Read(make([]byte, 64)); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("Read at end-of-stream = (%d, %v), want (0, io.EOF)", n, err)
	}
}

// TestSeekRecoversAfterFailedSeek checks that invalidation is not permanent: a
// caller that retries the seek on a healthy source gets a working decoder back,
// so the hard-fail state is recoverable rather than a one-way trip.
func TestSeekRecoversAfterFailedSeek(t *testing.T) {
	data, total := encodeRamp(t, 2, 16, 8*4096)
	src := newFailingSource(data)
	dec, err := NewDecoder(src)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	src.arm(0)
	if _, err := dec.SeekToSample(6 * 4096); !errors.Is(err, errSourceIO) {
		t.Fatalf("SeekToSample err = %v, want errSourceIO", err)
	}
	src.disarm()

	const target = 4 * 4096
	landed, err := dec.SeekToSample(target)
	if err != nil {
		t.Fatalf("retried SeekToSample: %v", err)
	}
	if landed != target {
		t.Fatalf("retried SeekToSample landed at %d, want %d", landed, target)
	}

	// The recovered decoder must produce exactly the tail the reference does.
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read after recovery: %v", err)
	}
	ref, err := NewDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("reference NewDecoder: %v", err)
	}
	if _, err := ref.SeekToSample(target); err != nil {
		t.Fatalf("reference SeekToSample: %v", err)
	}
	want, err := io.ReadAll(ref)
	if err != nil {
		t.Fatalf("reference read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("recovered decoder produced %d bytes, want %d identical bytes (total samples %d)",
			len(got), len(want), total)
	}
}

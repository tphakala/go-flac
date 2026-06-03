package pcm

import (
	"crypto/md5"
	"errors"
	"fmt"
	"hash"
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/frame"
	"github.com/tphakala/go-flac/internal/meta"
)

// Decoder decodes a FLAC stream into interleaved little-endian PCM. It implements
// io.Reader and io.WriterTo, and offers sample-accurate SeekToSample when the
// underlying source is an io.Seeker (implemented in M4).
type Decoder struct {
	br       *bitio.Reader
	rs       io.ReadSeeker // non-nil when the source is seekable
	info     flac.StreamInfo
	seekable bool

	audioStart   int64            // absolute byte offset of the first frame
	streamEnd    int64            // absolute file size
	nominalBlock int              // STREAMINFO max block size; fixed-blocksize nominal
	maxFrame     int              // STREAMINFO max frame size (0 unknown), seek-probe sizing
	seekPoints   []meta.SeekPoint // nil until M4b parsing (Task 8)

	frame      frame.Frame
	probeFrame frame.Frame // scratch for seek probes (Task 5)
	probeBuf   []byte      // reusable seek-probe read buffer (Task 5)
	buf        []byte      // packed PCM backing buffer, reused across frames
	pending    []byte      // unread window into buf
	bytesPS    int         // bytes per sample = ceil(bps/8)
	md5        hash.Hash
	decoded    uint64 // inter-channel samples decoded so far
	seeked     bool   // a seek happened; disables MD5 + truncation checks
	done       bool
	err        error
}

var (
	_ io.Reader   = (*Decoder)(nil)
	_ io.WriterTo = (*Decoder)(nil)
)

// NewDecoder reads the stream marker and metadata from r, returning a Decoder with
// Info populated.
func NewDecoder(r io.Reader) (*Decoder, error) {
	if r == nil {
		return nil, errors.New("go-flac/pcm: NewDecoder: nil reader")
	}
	rs, seekable := r.(io.ReadSeeker)
	var base int64
	if seekable {
		// Capture the absolute position where the FLAC stream begins, before any byte
		// is consumed, so audioStart is absolute even if r was advanced past a header.
		var err error
		if base, err = rs.Seek(0, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	br := bitio.NewReader(r)
	sm, err := meta.ReadMetadata(br)
	if err != nil {
		return nil, err
	}
	d := &Decoder{
		br:      br,
		info:    sm.Info,
		bytesPS: (sm.Info.BitDepth + 7) / 8,
		md5:     md5.New(),
	}
	if seekable {
		d.rs = rs
		d.seekable = true
		d.audioStart = base + br.BytesRead()
		resume, err := rs.Seek(0, io.SeekCurrent) // bufio-advanced absolute file pos
		if err != nil {
			return nil, err
		}
		if d.streamEnd, err = rs.Seek(0, io.SeekEnd); err != nil {
			return nil, err
		}
		if _, err = rs.Seek(resume, io.SeekStart); err != nil { // restore; bufio buffer stays valid
			return nil, err
		}
		d.nominalBlock = sm.MaxBlock
		d.maxFrame = sm.MaxFrame
		d.seekPoints = sm.SeekPoints
	}
	return d, nil
}

// Info returns the stream's STREAMINFO-derived properties.
func (d *Decoder) Info() flac.StreamInfo { return d.info }

// probeChunkDefault sizes seek-probe reads when STREAMINFO max frame size is unknown.
const probeChunkDefault = 1 << 18 // 256 KiB

// SeekToSample positions the decoder so the next Read/WriteTo yields audio starting at
// sampleIndex. It requires the source to be an io.Seeker. It returns the sample index
// positioned at (== sampleIndex on success). Seeking to or past a known TotalSamples,
// or past the true end of an unknown-length stream, positions at end-of-stream and
// returns the stream's total sample count (the next read is io.EOF). A negative index
// returns ErrInvalidSeek; a non-seekable source returns ErrSeekUnsupported.
func (d *Decoder) SeekToSample(sampleIndex int64) (int64, error) {
	if !d.seekable {
		return 0, flac.ErrSeekUnsupported
	}
	if sampleIndex < 0 {
		return 0, flac.ErrInvalidSeek
	}
	if total := int64(d.info.TotalSamples); total > 0 && sampleIndex >= total {
		return d.seekToEnd(total)
	}
	lo, hi := d.audioStart, d.streamEnd
	if len(d.seekPoints) > 0 {
		lo, hi = d.narrowBySeekTable(sampleIndex, lo, hi)
	}
	landStart, fs, endSample, err := d.searchFrame(sampleIndex, lo, hi)
	if err != nil {
		return 0, err
	}
	if landStart < 0 { // target is past the true end of an unknown-length stream
		return d.seekToEnd(endSample)
	}
	landed, err := d.land(landStart, sampleIndex, fs)
	if err != nil {
		return 0, err
	}
	return landed, nil
}

// searchFrame returns the byte offset and first sample of the frame containing target.
// landStart < 0 means target is past the stream end; endSample is then the total
// number of samples in the stream (last frame first sample + its block size).
func (d *Decoder) searchFrame(target, lo, hi int64) (landStart, fs, endSample int64, err error) {
	const linearWindow = 1 << 16 // bytes; below this, scan forward frame by frame
	for hi-lo > linearWindow {
		mid := lo + (hi-lo)/2
		start, end, ok, perr := d.probe(mid)
		if perr != nil {
			return 0, 0, 0, perr
		}
		if !ok { // no valid frame in [mid, hi): search lower
			hi = mid
			continue
		}
		f := d.firstSample(&d.probeFrame)
		switch {
		case f > target:
			hi = start
		case target >= f+int64(d.probeFrame.BlockSize):
			lo = end
		default:
			return start, f, 0, nil
		}
	}
	// Linear scan the small remaining window.
	b := lo
	for {
		start, end, ok, perr := d.probe(b)
		if perr != nil {
			return 0, 0, 0, perr
		}
		if !ok { // reached EOF without containing frame: target is past the true end
			return -1, 0, endSample, nil
		}
		f := d.firstSample(&d.probeFrame)
		endSample = f + int64(d.probeFrame.BlockSize)
		switch {
		case f <= target && target < endSample:
			return start, f, 0, nil
		case f > target: // overshot (rare): land at this frame's first sample
			return start, f, 0, nil
		default:
			b = end
		}
	}
}

// firstSample returns the first inter-channel sample index of a decoded frame.
func (d *Decoder) firstSample(fr *frame.Frame) int64 {
	if fr.VariableBlockSize {
		return int64(fr.Number) // coded sample number
	}
	return int64(fr.Number) * int64(d.nominalBlock) // frame number * nominal block size
}

// probe finds the first CRC-16-valid frame at or after absolute offset b, decoding it
// into d.probeFrame. It returns the frame's absolute start and end offsets, or ok=false
// when no complete frame remains before streamEnd. It does not disturb d.br.
func (d *Decoder) probe(b int64) (start, end int64, ok bool, err error) {
	window := int64(2 * d.maxFrame)
	if window < probeChunkDefault {
		window = probeChunkDefault
	}
	for b < d.streamEnd {
		n := d.streamEnd - b
		if n > window {
			n = window
		}
		if _, err = d.rs.Seek(b, io.SeekStart); err != nil {
			return 0, 0, false, err
		}
		if int64(cap(d.probeBuf)) < n {
			d.probeBuf = make([]byte, n)
		}
		buf := d.probeBuf[:n]
		if _, err = io.ReadFull(d.rs, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, 0, false, err
		}
		s, consumed, res := frame.FindNextFrame(buf, d.info, &d.probeFrame)
		switch res {
		case frame.FrameFound:
			return b + int64(s), b + int64(s+consumed), true, nil
		case frame.FrameTruncated:
			if b+n >= d.streamEnd {
				return 0, 0, false, nil // truncated at the true EOF: no complete frame
			}
			window *= 2 // grow and retry from the same base
		default: // FrameNotFound
			if b+n >= d.streamEnd {
				return 0, 0, false, nil
			}
			b += n - 1 // advance, overlapping 1 byte for a sync straddling the boundary
		}
	}
	return 0, 0, false, nil
}

// land seeks to landStart, decodes the containing frame, and drops (target-fs) leading
// inter-channel samples. It returns the sample index actually positioned at.
func (d *Decoder) land(landStart, target, fs int64) (int64, error) {
	if _, err := d.rs.Seek(landStart, io.SeekStart); err != nil {
		return 0, err
	}
	d.br = bitio.NewReaderAt(d.rs, landStart)
	d.seeked = true
	d.done = false
	d.err = nil
	if err := frame.Decode(d.br, d.info, &d.frame); err != nil {
		return 0, err
	}
	d.buf = appendPacked(d.buf[:0], &d.frame, d.bytesPS)
	if target < fs {
		target = fs // overshoot clamp: land at the frame start
	}
	drop := (target - fs) * int64(len(d.frame.Channels)) * int64(d.bytesPS)
	d.pending = d.buf[drop:]
	return target, nil
}

// seekToEnd positions the decoder at end-of-stream so the next read is io.EOF, and
// returns total (the stream's sample count).
func (d *Decoder) seekToEnd(total int64) (int64, error) {
	d.seeked = true
	d.done = true
	d.pending = nil
	d.err = nil
	return total, nil
}

// narrowBySeekTable tightens [lo, hi] using the bracketing SEEKTABLE points (M4b fills
// d.seekPoints; in M4a it is empty and this is never called).
func (d *Decoder) narrowBySeekTable(target, lo, hi int64) (newLo, newHi int64) {
	newLo, newHi = lo, hi
	for _, p := range d.seekPoints {
		off := d.audioStart + int64(p.ByteOffset)
		if int64(p.SampleNumber) <= target && off > newLo {
			newLo = off
		}
		if int64(p.SampleNumber) > target && off < newHi {
			newHi = off
		}
	}
	return newLo, newHi
}

// decodeNextFrame decodes one frame, packs its PCM into d.buf, and feeds MD5.
// d.pending is a window into d.buf that Read/WriteTo drain. Packing into d.buf
// (reset to d.buf[:0]) keeps the backing array stable across frames, so append
// reuses capacity instead of reallocating once it is warmed up. It returns io.EOF
// when the stream ends cleanly.
func (d *Decoder) decodeNextFrame() error {
	if d.done {
		return io.EOF
	}
	err := frame.Decode(d.br, d.info, &d.frame)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return d.finish() // clean end at a frame boundary
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Mid-frame cut: surface ErrTruncatedStream, keep io.ErrUnexpectedEOF in chain.
			err = fmt.Errorf("%w: %w", flac.ErrTruncatedStream, err)
		}
		d.err = err
		return err
	}
	d.decoded += uint64(d.frame.BlockSize)
	d.buf = appendPacked(d.buf[:0], &d.frame, d.bytesPS)
	if !d.seeked {
		d.md5.Write(d.buf) // MD5 is meaningless once frames have been skipped
	}
	d.pending = d.buf
	return nil
}

// finish verifies the stream MD5 and sample count (when not seeked) and marks
// the decoder done.
func (d *Decoder) finish() error {
	d.done = true
	if !d.seeked {
		var zero [16]byte
		if d.info.MD5 != zero {
			var sum [16]byte
			copy(sum[:], d.md5.Sum(nil))
			if sum != d.info.MD5 {
				d.err = flac.ErrMD5Mismatch
				return d.err
			}
		} else if d.info.TotalSamples != 0 && d.decoded < d.info.TotalSamples {
			d.err = flac.ErrTruncatedStream // inter-frame cut on a zero-MD5 stream
			return d.err
		}
	}
	return io.EOF
}

// Read fills p with interleaved little-endian PCM.
func (d *Decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		// Per io.Reader, a zero-length read returns immediately; do not decode
		// a frame just to copy nothing.
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}
	for len(d.pending) == 0 {
		if err := d.decodeNextFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

// WriteTo drains all decoded PCM into w.
func (d *Decoder) WriteTo(w io.Writer) (int64, error) {
	if d.err != nil {
		return 0, d.err
	}
	var total int64
	if len(d.pending) > 0 {
		n, err := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if err != nil {
			return total, err
		}
	}
	for {
		err := d.decodeNextFrame()
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		n, werr := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if werr != nil {
			return total, werr
		}
	}
}

// appendPacked appends the frame's interleaved little-endian PCM to dst. It grows
// dst once to the exact size and writes by index, so a warmed-up reused buffer
// packs each frame with no further allocation or per-byte append overhead.
func appendPacked(dst []byte, fr *frame.Frame, bytesPS int) []byte {
	nch := len(fr.Channels)
	oldLen := len(dst)
	newLen := oldLen + fr.BlockSize*nch*bytesPS
	if cap(dst) < newLen {
		grown := make([]byte, newLen)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:newLen]
	}
	idx := oldLen
	// Specialize on the byte width (1..4 for all FLAC bit depths) so the inner
	// loop and variable shift drop out of the per-sample hot path.
	switch bytesPS {
	case 1:
		for i := range fr.BlockSize {
			for ch := range nch {
				dst[idx] = byte(fr.Channels[ch][i])
				idx++
			}
		}
	case 2:
		for i := range fr.BlockSize {
			for ch := range nch {
				v := uint16(fr.Channels[ch][i])
				dst[idx] = byte(v)
				dst[idx+1] = byte(v >> 8)
				idx += 2
			}
		}
	case 3:
		for i := range fr.BlockSize {
			for ch := range nch {
				v := uint32(fr.Channels[ch][i])
				dst[idx] = byte(v)
				dst[idx+1] = byte(v >> 8)
				dst[idx+2] = byte(v >> 16)
				idx += 3
			}
		}
	case 4:
		for i := range fr.BlockSize {
			for ch := range nch {
				v := uint32(fr.Channels[ch][i])
				dst[idx] = byte(v)
				dst[idx+1] = byte(v >> 8)
				dst[idx+2] = byte(v >> 16)
				dst[idx+3] = byte(v >> 24)
				idx += 4
			}
		}
	default:
		for i := range fr.BlockSize {
			for ch := range nch {
				v := uint32(fr.Channels[ch][i])
				for b := range bytesPS {
					dst[idx] = byte(v >> (uint(b) * 8))
					idx++
				}
			}
		}
	}
	return dst
}

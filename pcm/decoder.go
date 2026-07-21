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
// io.Reader and io.WriterTo, and offers sample-accurate SeekToSample; seeking
// requires the underlying source to be an io.Seeker.
type Decoder struct {
	br       *bitio.Reader
	rs       io.ReadSeeker // non-nil when the source is seekable
	info     flac.StreamInfo
	seekable bool

	audioStart   int64            // absolute byte offset of the first frame
	streamEnd    int64            // absolute file size
	nominalBlock int              // STREAMINFO max block size; fixed-blocksize nominal
	maxFrame     int              // STREAMINFO max frame size (0 unknown), seek-probe sizing
	seekPoints   []meta.SeekPoint // parsed SEEKTABLE points (nil when absent); narrows seeks

	frame      frame.Frame
	probeFrame frame.Frame  // scratch frame decoded by seek probes
	probeBuf   []byte       // reusable seek-probe read buffer
	resyncR    bitio.Reader // reusable scratch reader for the resync scan (avoids per-probe alloc)
	buf        []byte       // packed PCM backing buffer, reused across frames
	pending    []byte       // unread window into buf
	bytesPS    int          // bytes per sample = ceil(bps/8)
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
		b, err := rs.Seek(0, io.SeekCurrent)
		if err != nil {
			seekable = false // advertises io.Seeker but cannot seek; decode forward-only
		} else {
			base = b
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
		audioStart := base + br.BytesRead()
		resume, serr := rs.Seek(0, io.SeekCurrent) // read-ahead-advanced absolute file pos
		var streamEnd int64
		if serr == nil {
			streamEnd, serr = rs.Seek(0, io.SeekEnd)
		}
		if serr == nil {
			if _, rerr := rs.Seek(resume, io.SeekStart); rerr != nil { // restore; owned read-ahead buffer stays valid
				// The cursor was advanced to EOF to measure length but cannot be restored,
				// so the reader can no longer forward-decode: surface this as a hard error.
				return nil, rerr
			}
			d.rs = rs
			d.seekable = true
			d.audioStart = audioStart
			d.streamEnd = streamEnd
			d.nominalBlock = sm.MaxBlock
			d.maxFrame = sm.MaxFrame
			d.seekPoints = sm.SeekPoints
		}
		// If serr != nil the source advertises io.Seeker but cannot be measured (no
		// SeekEnd support, etc.). The failed SeekCurrent/SeekEnd leaves the read cursor
		// in place, so the decoder degrades to forward-only (d.seekable stays false)
		// rather than failing construction.
	}
	return d, nil
}

// Info returns the stream's STREAMINFO-derived properties.
func (d *Decoder) Info() flac.StreamInfo { return d.info }

// probeChunkDefault sizes seek-probe reads when STREAMINFO max frame size is unknown.
const probeChunkDefault = 1 << 18 // 256 KiB

// maxProbeWindow caps a single seek-probe read. A real FLAC frame is far smaller, so
// this bounds memory if a malformed STREAMINFO max frame size or a crafted truncated
// frame would otherwise drive the probe window to the whole stream.
const maxProbeWindow = 1 << 24 // 16 MiB

// SeekToSample positions the decoder so the next Read/WriteTo yields audio starting at
// sampleIndex. It requires the source to be an io.Seeker. It returns the sample index
// positioned at (== sampleIndex on success). Seeking to or past a known TotalSamples,
// or past the true end of an unknown-length stream, positions at end-of-stream and
// returns the stream's total sample count (the next read is io.EOF). A negative index
// returns ErrInvalidSeek; a non-seekable source returns ErrSeekUnsupported.
//
// A seek that fails after those two argument checks leaves the decoder in a hard-failed
// state, whether the failure came from the source (an I/O error while probing) or from
// the stream itself (a frame that will not decode at the landing offset). The error is
// sticky, so a subsequent Read or WriteTo returns it rather than resuming from an
// indeterminate position. Retrying the seek, once the condition that caused it is gone,
// clears the state. ErrInvalidSeek and ErrSeekUnsupported are argument and capability
// errors that touch no state, so they leave the decoder readable.
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
	if d.nominalBlock <= 0 {
		// STREAMINFO did not record a max block size (MaxBlock == 0). Discover the
		// fixed-blocksize nominal block size from the first frame so frame numbers map to
		// sample numbers; for a variable-blocksize stream it stays 0 and is unused.
		if err := d.ensureNominalBlock(); err != nil {
			return d.seekFailed(err)
		}
	}
	lo, hi := d.audioStart, d.streamEnd
	if len(d.seekPoints) > 0 {
		lo, hi = d.narrowBySeekTable(sampleIndex, lo, hi)
	}
	landStart, fs, endSample, err := d.searchFrame(sampleIndex, lo, hi)
	if err != nil {
		return d.seekFailed(err)
	}
	if landStart < 0 { // target is past the true end of an unknown-length stream
		return d.seekToEnd(endSample)
	}
	landed, err := d.land(landStart, sampleIndex, fs)
	if err != nil {
		return d.seekFailed(err)
	}
	if total := int64(d.info.TotalSamples); total > 0 && landed > total {
		// A corrupt stream whose frame numbers disagree with the declared STREAMINFO total
		// can overshoot past it; never report a position beyond the stream's bounds.
		landed = total
	}
	return landed, nil
}

// seekFailed marks the decoder unusable after a seek failed part-way, and returns the
// error for SeekToSample to propagate. Every stage past the argument checks (the probes
// and the landing decode) repositions the shared d.rs cursor, so on failure d.br's
// buffered bytes and logical position no longer correspond to the source, and d.pending
// may hold samples from the abandoned position. A caller that treats the seek error as
// non-fatal and reads on would otherwise resume from the wrong offset and get silently
// corrupted audio, so the error is made sticky instead: Read and WriteTo both surface
// d.err before touching the reader. Dropping d.br keeps a stale reader from being used
// even if some future path skips that gate. The state is recoverable, not terminal: a
// later successful seek rebuilds d.br and clears d.err in land.
func (d *Decoder) seekFailed(err error) (int64, error) {
	d.err = err
	d.pending = nil
	d.br = nil
	return 0, err
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
			if start >= hi {
				hi = mid // [mid, hi) holds no frame; the target frame is below mid
			} else {
				hi = start
			}
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
	if window > maxProbeWindow {
		window = maxProbeWindow
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
		rn, rerr := io.ReadFull(d.rs, buf)
		if rerr != nil && !errors.Is(rerr, io.ErrUnexpectedEOF) && !errors.Is(rerr, io.EOF) {
			return 0, 0, false, rerr
		}
		buf = buf[:rn] // only the bytes actually read; the reused buffer tail is stale
		// A short read means the source ended before the measured streamEnd (truncated
		// since NewDecoder measured it), so this is the physical end of the stream.
		atEnd := b+int64(rn) >= d.streamEnd || int64(rn) < n
		s, consumed, res := frame.FindNextFrame(&d.resyncR, buf, d.info, &d.probeFrame)
		switch res {
		case frame.FrameFound:
			return b + int64(s), b + int64(s+consumed), true, nil
		case frame.FrameTruncated:
			if atEnd || window >= maxProbeWindow {
				// At the true EOF, or no complete frame fits the bounded probe window (a
				// real FLAC frame is far smaller, so this is malformed): give up.
				return 0, 0, false, nil
			}
			window *= 2 // grow and retry from the same base
			if window > maxProbeWindow {
				window = maxProbeWindow
			}
		default: // FrameNotFound
			if atEnd {
				return 0, 0, false, nil
			}
			b += int64(rn) - 1 // advance, overlapping 1 byte for a sync straddling the boundary
		}
	}
	return 0, 0, false, nil
}

// ensureNominalBlock discovers the fixed-blocksize nominal block size from the first
// audio frame when STREAMINFO did not record a max block size (MaxBlock == 0). For a
// variable-blocksize stream the nominal block size is unused, so it is left at zero.
func (d *Decoder) ensureNominalBlock() error {
	_, _, ok, err := d.probe(d.audioStart)
	if err != nil {
		return err
	}
	if ok && !d.probeFrame.VariableBlockSize {
		d.nominalBlock = d.probeFrame.BlockSize
	}
	return nil
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

// narrowBySeekTable tightens [lo, hi] using the bracketing SEEKTABLE points. When no
// SEEKTABLE was present d.seekPoints is empty and this is never called.
func (d *Decoder) narrowBySeekTable(target, lo, hi int64) (newLo, newHi int64) {
	newLo, newHi = lo, hi
	for _, p := range d.seekPoints {
		// Ignore a corrupt seek point whose byte offset is negative (overflow on the
		// uint64 -> int64 conversion) or points outside the audio region, so it cannot
		// push the search bounds out of [audioStart, streamEnd].
		if int64(p.ByteOffset) < 0 || int64(p.ByteOffset) > d.streamEnd-d.audioStart {
			continue
		}
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

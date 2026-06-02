package pcm

import (
	"crypto/md5"
	"errors"
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
	info     flac.StreamInfo
	seekable bool

	frame   frame.Frame
	buf     []byte // packed PCM backing buffer, reused across frames
	pending []byte // unread window into buf, not yet returned by Read
	bytesPS int    // bytes per sample = ceil(bps/8)
	md5     hash.Hash
	done    bool
	err     error
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
	br := bitio.NewReader(r)
	si, err := meta.ReadMetadata(br)
	if err != nil {
		return nil, err
	}
	d := &Decoder{
		br:      br,
		info:    si,
		bytesPS: (si.BitDepth + 7) / 8,
		md5:     md5.New(),
	}
	_, d.seekable = r.(io.Seeker)
	return d, nil
}

// Info returns the stream's STREAMINFO-derived properties.
func (d *Decoder) Info() flac.StreamInfo { return d.info }

// SeekToSample moves the read position to the given inter-channel sample index.
// Implemented in M4; until then it reports ErrSeekUnsupported for non-seekable
// sources and a not-implemented error for seekable ones.
func (d *Decoder) SeekToSample(sampleIndex int64) (int64, error) {
	_ = sampleIndex // used once seeking lands in M4
	if !d.seekable {
		return 0, flac.ErrSeekUnsupported
	}
	return 0, flac.ErrNotImplemented
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
		// Only a clean io.EOF at a frame boundary ends the stream; a mid-frame
		// io.ErrUnexpectedEOF is a truncation and propagates as a real error.
		if errors.Is(err, io.EOF) {
			return d.finish()
		}
		d.err = err
		return err
	}
	d.buf = appendPacked(d.buf[:0], &d.frame, d.bytesPS)
	d.md5.Write(d.buf)
	d.pending = d.buf
	return nil
}

// finish verifies the stream MD5 (if present) and marks the decoder done.
func (d *Decoder) finish() error {
	d.done = true
	var zero [16]byte
	if d.info.MD5 != zero {
		var sum [16]byte
		copy(sum[:], d.md5.Sum(nil))
		if sum != d.info.MD5 {
			d.err = flac.ErrMD5Mismatch
			return d.err
		}
	}
	return io.EOF
}

// Read fills p with interleaved little-endian PCM.
func (d *Decoder) Read(p []byte) (int, error) {
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

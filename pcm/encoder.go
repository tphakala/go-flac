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

const encoderBlockSize = 4096

// Encoder encodes interleaved little-endian PCM written to it into a FLAC stream.
// It implements io.WriteCloser; Close flushes the final frame and finalizes the
// STREAMINFO MD5, total samples, and min/max sizes (the last only when the sink is
// an io.WriteSeeker; otherwise spec-legal "unknown" sentinels remain).
type Encoder struct {
	w        io.Writer
	ws       io.WriteSeeker // non-nil when w is seekable
	cfg      Config
	si       flac.StreamInfo // stream properties, built once from cfg
	params   frame.Params
	bytesPS  int
	frameLen int // bytesPS * channels (bytes per inter-channel sample)

	bw  *bitio.Writer
	ch  [][]int32 // per-channel block buffers (len encoderBlockSize)
	md5 hash.Hash

	carry    []byte // reusable join buffer for leftover + new input
	leftover []byte // < one full block of trailing bytes between Writes

	frameNum           uint64
	total              uint64
	wrote              bool
	minBlock, maxBlock int
	minFrame, maxFrame int

	closed bool
}

var _ io.WriteCloser = (*Encoder)(nil)

// NewEncoder returns an Encoder that writes a FLAC stream to w using cfg. It writes
// the stream marker and a placeholder STREAMINFO immediately. M3 supports bit
// depths 4..24; other depths are rejected.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("go-flac/pcm: NewEncoder: nil writer")
	}
	if cfg.SampleRate <= 0 || cfg.SampleRate > 655350 || cfg.Channels < 1 || cfg.Channels > 8 {
		// 655350 Hz is the FLAC maximum; the STREAMINFO sample-rate field is 20 bits,
		// so a larger rate would be silently truncated.
		return nil, fmt.Errorf("go-flac/pcm: NewEncoder: invalid config %+v", cfg)
	}
	if cfg.BitDepth < 4 || cfg.BitDepth > 24 {
		return nil, fmt.Errorf("go-flac/pcm: NewEncoder: bit depth %d outside supported 4..24 (M3)", cfg.BitDepth)
	}

	e := &Encoder{
		w:       w,
		cfg:     cfg,
		si:      flac.StreamInfo{SampleRate: cfg.SampleRate, Channels: cfg.Channels, BitDepth: cfg.BitDepth},
		params:  paramsForLevel(cfg.CompressionLevel),
		bytesPS: (cfg.BitDepth + 7) / 8,
		bw:      bitio.NewWriter(),
		md5:     md5.New(),
	}
	e.frameLen = e.bytesPS * cfg.Channels
	e.ch = make([][]int32, cfg.Channels)
	for c := range e.ch {
		e.ch[c] = make([]int32, encoderBlockSize)
	}
	if ws, ok := w.(io.WriteSeeker); ok {
		e.ws = ws
	}

	body := meta.EncodeStreamInfo(e.si, 0, 0, 0, 0) // placeholders
	if err := meta.WriteStreamHeader(w, body); err != nil {
		return nil, err
	}
	return e, nil
}

// Write consumes interleaved little-endian PCM samples. Bytes that do not yet form
// a full 4096-sample block are buffered until the next Write or Close. The join
// buffer (e.carry) only ever assembles the single block straddling the leftover/p
// boundary; all further whole blocks are read straight from p, so neither e.carry
// nor e.leftover grows past single-block scale regardless of how large p is.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, flac.ErrEncoderClosed
	}
	blockBytes := encoderBlockSize * e.frameLen
	n := len(p) // captured before p is resliced below; Write must report this

	// 1. Complete one block from leftover + the head of p, if we now have enough.
	if len(e.leftover) > 0 {
		need := blockBytes - len(e.leftover) // >= 1: leftover is always < blockBytes
		if len(p) < need {                   // still short of a full block
			e.leftover = append(e.leftover, p...)
			return n, nil
		}
		e.carry = append(e.carry[:0], e.leftover...)
		e.carry = append(e.carry, p[:need]...) // e.carry is now exactly one block
		if err := e.emitBlock(e.carry, encoderBlockSize); err != nil {
			return 0, err
		}
		e.leftover = e.leftover[:0]
		p = p[need:]
	}

	// 2. Emit whole blocks straight from p (no copy).
	off := 0
	for len(p)-off >= blockBytes {
		if err := e.emitBlock(p[off:off+blockBytes], encoderBlockSize); err != nil {
			return 0, err
		}
		off += blockBytes
	}

	// 3. Stash the remainder (< one block) as leftover.
	e.leftover = append(e.leftover[:0], p[off:]...)
	return n, nil
}

// emitBlock feeds chunk (exactly n inter-channel samples) into MD5, deinterleaves
// it, and writes one frame.
func (e *Encoder) emitBlock(chunk []byte, n int) error {
	e.md5.Write(chunk)
	for c := range e.ch {
		e.ch[c] = e.ch[c][:n]
	}
	deinterleaveSamples(e.ch, chunk, n, e.cfg.Channels, e.bytesPS)

	buf := frame.EncodeFrame(e.bw, e.params, e.si, e.ch, e.frameNum)
	if _, err := e.w.Write(buf); err != nil {
		return err
	}
	e.frameNum++
	e.total += uint64(n)
	sz := len(buf)
	if !e.wrote {
		e.minFrame, e.maxFrame, e.minBlock, e.maxBlock, e.wrote = sz, sz, n, n, true
	} else {
		e.minFrame = min(e.minFrame, sz)
		e.maxFrame = max(e.maxFrame, sz)
		e.minBlock = min(e.minBlock, n)
		e.maxBlock = max(e.maxBlock, n)
	}
	// Restore full-length buffers for the next block.
	for c := range e.ch {
		e.ch[c] = e.ch[c][:encoderBlockSize]
	}
	return nil
}

// Close flushes the final (possibly short) block and finalizes STREAMINFO. It is
// idempotent.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true

	if len(e.leftover) > 0 {
		if len(e.leftover)%e.frameLen != 0 {
			return fmt.Errorf("go-flac/pcm: Close: %d trailing bytes are not a whole sample", len(e.leftover))
		}
		n := len(e.leftover) / e.frameLen
		if err := e.emitBlock(e.leftover, n); err != nil {
			return err
		}
	}
	e.leftover = nil

	if e.ws == nil {
		return nil // non-seekable: keep the unknown sentinels written up front
	}
	si := e.si
	si.TotalSamples = e.total
	copy(si.MD5[:], e.md5.Sum(nil))
	body := meta.EncodeStreamInfo(si, e.minBlock, e.maxBlock, e.minFrame, e.maxFrame)
	if _, err := e.ws.Seek(int64(meta.StreamInfoBodyOffset), io.SeekStart); err != nil {
		return fmt.Errorf("go-flac/pcm: Close: seek to STREAMINFO: %w", err)
	}
	if _, err := e.ws.Write(body); err != nil {
		return fmt.Errorf("go-flac/pcm: Close: patch STREAMINFO: %w", err)
	}
	if _, err := e.ws.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("go-flac/pcm: Close: seek to end: %w", err)
	}
	return nil
}

// paramsForLevel maps a compression level (clamped to 0..8) to frame parameters.
// Levels 0..2 are fully realized in M3 (fixed-only); levels 3..8 carry the same
// fixed-only-relevant knobs and light up further when LPC lands in M3b.
func paramsForLevel(level int) frame.Params {
	if level < 0 {
		level = 0
	}
	if level > 8 {
		level = 8
	}
	type row struct {
		stereo  frame.StereoMode
		maxPart int
		exFixed bool
	}
	table := [9]row{
		0: {frame.StereoIndependent, 3, false},
		1: {frame.StereoAdaptive, 3, false},
		2: {frame.StereoFull, 3, false},
		3: {frame.StereoFull, 4, false},
		4: {frame.StereoFull, 4, true},
		5: {frame.StereoFull, 5, true},
		6: {frame.StereoFull, 6, true},
		7: {frame.StereoFull, 6, true},
		8: {frame.StereoFull, 6, true},
	}
	r := table[level]
	return frame.Params{Stereo: r.stereo, MaxPartitionOrder: r.maxPart, ExhaustiveFixed: r.exFixed}
}

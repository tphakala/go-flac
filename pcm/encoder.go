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

// defaultSeekMaxPoints sizes the reserved SEEKTABLE when SeekTableMaxPoints is 0.
// 4096 points is 4096*18 = 72 KiB, ample for typical files at one point per second.
const defaultSeekMaxPoints = 4096

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

	seekInterval  int   // samples between seek points (0 disables)
	seekMaxPoints int   // reserved placeholder point count
	seekBodyOff   int64 // absolute byte offset of the SEEKTABLE body
	audioBytes    int64 // audio bytes written so far (= next frame's byte offset)
	points        []meta.SeekPoint
	nextBoundary  int64 // next sample boundary at which to record a point

	closed bool
}

var _ io.WriteCloser = (*Encoder)(nil)

// NewEncoder returns an Encoder that writes a FLAC stream to w using cfg. It writes
// the stream marker and a placeholder STREAMINFO immediately. Supported bit depths
// are 4..32; other depths are rejected.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("go-flac/pcm: NewEncoder: nil writer")
	}
	if cfg.SampleRate <= 0 || cfg.SampleRate > 655350 || cfg.Channels < 1 || cfg.Channels > 8 {
		// 655350 Hz is the FLAC maximum; the STREAMINFO sample-rate field is 20 bits,
		// so a larger rate would be silently truncated.
		return nil, fmt.Errorf("go-flac/pcm: NewEncoder: invalid config %+v", cfg)
	}
	if cfg.BitDepth < 4 || cfg.BitDepth > 32 {
		return nil, fmt.Errorf("go-flac/pcm: NewEncoder: bit depth %d outside supported 4..32", cfg.BitDepth)
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

	if cfg.SeekTableInterval > 0 {
		if e.ws == nil {
			return nil, errors.New("go-flac/pcm: NewEncoder: SeekTableInterval requires an io.WriteSeeker sink")
		}
		e.seekInterval = cfg.SeekTableInterval
		e.seekMaxPoints = cfg.SeekTableMaxPoints
		if e.seekMaxPoints <= 0 {
			e.seekMaxPoints = defaultSeekMaxPoints
		}
		// A metadata block length is 24 bits, so the SEEKTABLE body (seekMaxPoints points
		// of SeekPointBytes each) must fit; clamp to the limit rather than silently
		// truncating the length into a corrupt header.
		if maxPoints := (1<<24 - 1) / meta.SeekPointBytes; e.seekMaxPoints > maxPoints {
			e.seekMaxPoints = maxPoints
		}
		// Reserve the points slice up front (only when a seek table was requested) so a
		// long encode does not repeatedly grow it; it never exceeds seekMaxPoints.
		e.points = make([]meta.SeekPoint, 0, e.seekMaxPoints)
		e.nextBoundary = int64(e.seekInterval)
		siBody := meta.EncodeStreamInfo(e.si, 0, 0, 0, 0)
		if err := meta.WriteStreamHeaderEx(w, siBody, false); err != nil { // last=0
			return nil, err
		}
		stBody := meta.SeekTablePlaceholder(e.seekMaxPoints)
		if _, err := w.Write(meta.EncodeBlockHeader(false, meta.TypeSeekTable, len(stBody))); err != nil {
			return nil, err
		}
		// SEEKTABLE body offset = "fLaC" + STREAMINFO header (StreamInfoBodyOffset) +
		// STREAMINFO body + SEEKTABLE header (4).
		e.seekBodyOff = int64(meta.StreamInfoBodyOffset + meta.StreamInfoBodyLen + 4)
		if _, err := w.Write(stBody); err != nil {
			return nil, err
		}
		if _, err := w.Write(meta.EncodeBlockHeader(true, meta.TypePadding, 0)); err != nil { // last=1
			return nil, err
		}
	} else {
		body := meta.EncodeStreamInfo(e.si, 0, 0, 0, 0) // placeholders, last=1
		if err := meta.WriteStreamHeader(w, body); err != nil {
			return nil, err
		}
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

	// written counts bytes of p that landed in blocks already handed to the sink,
	// so a mid-write emitBlock failure reports a contract-correct partial count
	// (io.Writer requires the bytes consumed from p, not 0) instead of 0.
	written := 0

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
			return 0, err // boundary block failed: no bytes of p durably consumed
		}
		e.leftover = e.leftover[:0]
		p = p[need:]
		written = need
	}

	// 2. Emit whole blocks straight from p (no copy).
	off := 0
	for len(p)-off >= blockBytes {
		if err := e.emitBlock(p[off:off+blockBytes], encoderBlockSize); err != nil {
			return written + off, err
		}
		off += blockBytes
	}

	// 3. Stash the remainder (< one block) as leftover.
	e.leftover = append(e.leftover[:0], p[off:]...)
	return n, nil
}

// emitBlock feeds chunk (exactly n inter-channel samples) into MD5, deinterleaves
// it, and writes one frame. When seek-table recording is active, a seek point is
// appended for the first frame (frameOffset == 0) and for each frame whose first
// sample meets or crosses the next boundary.
func (e *Encoder) emitBlock(chunk []byte, n int) error {
	e.md5.Write(chunk)
	for c := range e.ch {
		e.ch[c] = e.ch[c][:n]
	}
	deinterleaveSamples(e.ch, chunk, n, e.cfg.Channels, e.bytesPS)

	frameOffset := e.audioBytes // byte offset of this frame from the first frame
	firstSample := e.total      // first inter-channel sample of this block

	buf := frame.EncodeFrame(e.bw, e.params, e.si, e.ch, e.frameNum)
	if _, err := e.w.Write(buf); err != nil {
		return err
	}
	if e.seekInterval > 0 && len(e.points) < e.seekMaxPoints {
		if frameOffset == 0 || int64(firstSample) >= e.nextBoundary {
			e.points = append(e.points, meta.SeekPoint{
				SampleNumber: firstSample,
				ByteOffset:   uint64(frameOffset),
				FrameSamples: uint16(n),
			})
			for int64(firstSample) >= e.nextBoundary { // skip boundaries this block passed
				e.nextBoundary += int64(e.seekInterval)
			}
		}
	}
	e.audioBytes += int64(len(buf))

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

	if e.seekInterval > 0 {
		used := len(e.points)
		if _, err := e.ws.Seek(e.seekBodyOff, io.SeekStart); err != nil {
			return fmt.Errorf("go-flac/pcm: Close: seek to SEEKTABLE: %w", err)
		}
		if _, err := e.ws.Write(meta.EncodeSeekPoints(e.points)); err != nil {
			return fmt.Errorf("go-flac/pcm: Close: write seek points: %w", err)
		}
		if used < e.seekMaxPoints {
			// Shrink the SEEKTABLE block to the used points and grow PADDING to keep
			// the audio offset fixed (spec section 4.4).
			if _, err := e.ws.Seek(e.seekBodyOff-4, io.SeekStart); err != nil {
				return fmt.Errorf("go-flac/pcm: Close: seek to SEEKTABLE header: %w", err)
			}
			if _, err := e.ws.Write(meta.EncodeBlockHeader(false, meta.TypeSeekTable, used*meta.SeekPointBytes)); err != nil {
				return fmt.Errorf("go-flac/pcm: Close: shrink SEEKTABLE: %w", err)
			}
			if _, err := e.ws.Seek(e.seekBodyOff+int64(used*meta.SeekPointBytes), io.SeekStart); err != nil {
				return fmt.Errorf("go-flac/pcm: Close: seek to PADDING: %w", err)
			}
			padLen := (e.seekMaxPoints - used) * meta.SeekPointBytes // (N-used)*18, exact (spec section 4.4)
			if _, err := e.ws.Write(meta.EncodeBlockHeader(true, meta.TypePadding, padLen)); err != nil {
				return fmt.Errorf("go-flac/pcm: Close: write PADDING header: %w", err)
			}
		}
		// used == seekMaxPoints: the full SEEKTABLE (last=0) + the pre-written empty
		// PADDING (last=1) are already correct; only the points needed patching.
	}

	if _, err := e.ws.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("go-flac/pcm: Close: seek to end: %w", err)
	}
	return nil
}

// paramsForLevel maps a compression level (clamped to 0..8) to frame parameters.
// Levels 0..2 use fixed predictors only (MaxLPCOrder = 0). Levels 3..8 enable
// LPC with increasing max order, matching libFLAC's -l values: l3=6, l4-6=8, l7-8=12.
// Levels 7 and 8 share all parameters in M3b (subdivide_tukey is deferred to M4).
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
		maxLPC  int
		prec    int
	}
	table := [9]row{
		0: {frame.StereoIndependent, 3, false, 0, 0},
		1: {frame.StereoAdaptive, 3, false, 0, 0},
		2: {frame.StereoFull, 3, false, 0, 0},
		3: {frame.StereoFull, 4, false, 6, 15},
		4: {frame.StereoFull, 4, true, 8, 15},
		5: {frame.StereoFull, 5, true, 8, 15},
		6: {frame.StereoFull, 6, true, 8, 15},
		7: {frame.StereoFull, 6, true, 12, 15},
		8: {frame.StereoFull, 6, true, 12, 15},
	}
	r := table[level]
	return frame.Params{
		Stereo:            r.stereo,
		MaxPartitionOrder: r.maxPart,
		ExhaustiveFixed:   r.exFixed,
		MaxLPCOrder:       r.maxLPC,
		LPCPrecision:      r.prec,
		// Apodization left as the zero value (ApodTukey05).
	}
}

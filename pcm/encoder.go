package pcm

import (
	"crypto/md5"
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

// maxTotalSamples is the largest value the STREAMINFO total_samples field can hold:
// it is 36 bits wide (see meta.EncodeStreamInfo), so a larger declared count would
// be silently truncated by the bit writer.
const maxTotalSamples = 1<<36 - 1

// Encoder encodes interleaved little-endian PCM written to it into a FLAC stream.
// It implements io.WriteCloser; Close flushes the final frame and finalizes the
// STREAMINFO MD5, total samples, and min/max block and frame sizes when the sink is
// an io.WriteSeeker. For a non-seekable sink, total_samples is still finalized up
// front when Config.TotalSamples is declared, and the min/max block size is always
// finalized up front to a spec-legal value (the encoder uses one fixed block size);
// only the MD5 and the min/max frame byte sizes keep their "unknown" sentinels,
// since those are not knowable without seeking back. EncodeInterleaved, which holds
// the whole buffer, additionally finalizes MD5 up front for a non-seekable sink.
type Encoder struct {
	w        io.Writer
	ws       io.WriteSeeker // non-nil when w is seekable
	cfg      Config
	si       flac.StreamInfo // stream properties, built once from cfg
	params   frame.Params
	bytesPS  int
	frameLen int // bytesPS * channels (bytes per inter-channel sample)

	bw   *bitio.Writer
	ch   [][]int32 // per-channel block buffers (len encoderBlockSize)
	work *frame.Workspace
	md5  hash.Hash

	carry    []byte // reusable join buffer for leftover + new input
	leftover []byte // < one full block of trailing bytes between Writes

	frameNum           uint64
	total              uint64
	wrote              bool
	skipHash           bool // STREAMINFO MD5 supplied up front (one-shot path): skip per-frame hashing
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
	e := &Encoder{}
	if err := e.init("NewEncoder", w, cfg, nil); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset rebinds the encoder to a new sink w and reconfigures it with cfg so a
// single Encoder can encode many independent streams without re-allocating its
// large internal buffers between them. It re-validates cfg exactly like
// NewEncoder, discards any input buffered from a previous (unflushed) stream,
// resets all per-stream state (MD5, frame/sample counters, min/max sizes, seek
// table), and re-emits the stream marker plus placeholder STREAMINFO (and the
// SEEKTABLE placeholder, when requested) to w. The expensive per-channel block
// buffers and frame workspace are retained and reused whenever the new config
// keeps the same channel count and LPC order; they are reallocated only when that
// shape changes. A same-shape Reset is therefore essentially allocation-free,
// which is what lets callers pool encoders (for example via sync.Pool) across a
// stream of short clips.
//
// After a successful Reset the encoder is ready for Write/Close as if freshly
// constructed; on error it must not be used. Reset may be called on a closed
// encoder, which is the usual pooling pattern (Reset, Write, Close, repeat).
func (e *Encoder) Reset(w io.Writer, cfg Config) error {
	return e.init("Reset", w, cfg, nil)
}

// minStreamInfoBlockSize is the smallest value the STREAMINFO min/max block-size
// fields may hold: RFC 9639 requires both to be in the 16..65535 range, so 0 and
// 1..15 are illegal there even though the encoder can legitimately emit a final
// block smaller than 16.
const minStreamInfoBlockSize = 16

// upfrontBlockSize returns the STREAMINFO min/max block size to finalize before any
// frame is written, or 0 for a seekable sink (which is patched with the measured
// value in Close instead). Only a non-seekable sink needs a finalized value, since
// its header cannot be patched later.
//
// The encoder uses one fixed block size (encoderBlockSize). RFC 9639 requires the
// advertised min/max to be in 16..65535 and explicitly allows them to be wider than
// the blocks actually written ("an encoder ... cannot know beforehand what block
// sizes it will use, only between what bounds"); the last, possibly-short block is
// exempt from the minimum. So for a non-seekable sink:
//   - an undeclared or zero length advertises encoderBlockSize, the only block size
//     this encoder emits (a single short stream is one last block, which is exempt);
//   - a declared length tightens the bound to min(total, encoderBlockSize) so a
//     single sub-block stream advertises its true size, matching the value the
//     seekable path measures and patches in Close;
//   - the result is floored to minStreamInfoBlockSize so a sub-16-sample declared
//     length still yields a spec-legal field rather than 0 or 1..15; Close applies
//     the same floor to its measured value, so the two sinks stay in agreement.
//
// Leaving these at the illegal 0 sentinel is what made libFLAC warn "sample or frame
// number does not increase correctly" and made strict decoders (Apple CoreAudio,
// browser Web Audio) reject the stream: they derive a fixed-blocksize frame's sample
// number as frame_number * max_blocksize, so a zero collapses every frame to sample
// 0.
func upfrontBlockSize(seekable bool, cfg Config) int {
	if seekable {
		return 0
	}
	blk := encoderBlockSize
	if cfg.TotalSamples > 0 {
		blk = int(min(cfg.TotalSamples, uint64(encoderBlockSize)))
	}
	return max(blk, minStreamInfoBlockSize)
}

// validateConfig checks the codec-relevant Config fields shared by the streaming
// Encoder and the FrameEncoder. op names the calling API for the error message.
func validateConfig(op string, cfg Config) error {
	if cfg.SampleRate <= 0 || cfg.SampleRate > 655350 || cfg.Channels < 1 || cfg.Channels > 8 {
		// 655350 Hz is the FLAC maximum; the STREAMINFO sample-rate field is 20 bits,
		// so a larger rate would be silently truncated.
		return fmt.Errorf("go-flac/pcm: %s: invalid config %+v", op, cfg)
	}
	if cfg.BitDepth < 4 || cfg.BitDepth > 32 {
		return fmt.Errorf("go-flac/pcm: %s: bit depth %d outside supported 4..32", op, cfg.BitDepth)
	}
	if cfg.TotalSamples > maxTotalSamples {
		// Reject a declared total that would not fit the 36-bit STREAMINFO field.
		return fmt.Errorf("go-flac/pcm: %s: total samples %d exceeds the FLAC maximum of 2^36-1", op, cfg.TotalSamples)
	}
	return nil
}

// init (re)initializes e to write a fresh FLAC stream to w using cfg. It backs
// NewEncoder (on a zero-valued Encoder), Reset (on a previously used one), and the
// one-shot EncodeInterleaved path; op names the calling API for error messages.
// knownMD5, when non-nil, is the whole-input STREAMINFO MD5 precomputed by the
// one-shot path: it is written into the placeholder up front and the per-frame
// hashing is skipped (it would only recompute the same digest). The large buffers
// (ch, work) are reused when the channel count and LPC order are unchanged from the
// previous configuration, and the shape-independent bw and md5 are reused once
// allocated, so a same-shape re-init allocates only the small fixed metadata header.
func (e *Encoder) init(op string, w io.Writer, cfg Config, knownMD5 *[16]byte) error {
	if w == nil {
		return fmt.Errorf("go-flac/pcm: %s: nil writer", op)
	}
	if err := validateConfig(op, cfg); err != nil {
		return err
	}

	params := paramsForLevel(cfg.CompressionLevel)

	// Decide buffer reuse against the PREVIOUS shape, still held in e.cfg/e.params,
	// before those fields are overwritten below. On a fresh encoder e.ch/e.work are
	// nil, so both guards fall through to allocation. Channel count and MaxLPCOrder
	// fully determine the workspace shape: the block size is the constant
	// encoderBlockSize, and paramsForLevel always leaves apodization at the Tukey(0.5)
	// default, so the workspace's window cache stays valid across a same-shape reuse.
	sameChannels := e.ch != nil && e.cfg.Channels == cfg.Channels
	sameWorkspace := e.work != nil && e.cfg.Channels == cfg.Channels && e.params.MaxLPCOrder == params.MaxLPCOrder

	e.w = w
	e.ws = nil
	if ws, ok := w.(io.WriteSeeker); ok {
		e.ws = ws
	}
	e.cfg = cfg
	// TotalSamples (when declared) and a precomputed MD5 (from the one-shot
	// EncodeInterleaved path) are folded into the up-front STREAMINFO placeholder so
	// a non-seekable sink emits a finalized header with no seek-back. Both placeholder
	// writers below read e.si, so the values flow into whichever branch runs.
	e.si = flac.StreamInfo{SampleRate: cfg.SampleRate, Channels: cfg.Channels, BitDepth: cfg.BitDepth, TotalSamples: cfg.TotalSamples}
	e.skipHash = knownMD5 != nil
	if e.skipHash {
		e.si.MD5 = *knownMD5
	}
	e.params = params
	e.bytesPS = (cfg.BitDepth + 7) / 8
	e.frameLen = e.bytesPS * cfg.Channels

	if !sameChannels {
		e.ch = make([][]int32, cfg.Channels)
		for c := range e.ch {
			e.ch[c] = make([]int32, encoderBlockSize)
		}
	}
	if !sameWorkspace {
		e.work = frame.NewWorkspace(encoderBlockSize, cfg.Channels, params.MaxLPCOrder)
	}
	if e.bw == nil {
		e.bw = bitio.NewWriter()
	} else {
		e.bw.Reset()
	}
	if e.md5 == nil {
		e.md5 = md5.New()
	} else {
		e.md5.Reset()
	}

	// Reset every per-stream field. carry/leftover/points keep their backing arrays
	// (truncated to zero length) so a reused encoder stays allocation-free.
	e.carry = e.carry[:0]
	e.leftover = e.leftover[:0]
	e.frameNum = 0
	e.total = 0
	e.wrote = false
	e.minBlock, e.maxBlock, e.minFrame, e.maxFrame = 0, 0, 0, 0
	e.audioBytes = 0
	e.closed = false
	e.seekInterval = 0
	e.seekMaxPoints = 0
	e.seekBodyOff = 0
	e.nextBoundary = 0
	e.points = e.points[:0]

	if cfg.SeekTableInterval > 0 {
		if e.ws == nil {
			return fmt.Errorf("go-flac/pcm: %s: SeekTableInterval requires an io.WriteSeeker sink", op)
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
		// Reserve the points slice up front (reusing the backing array when it is
		// already large enough) so a long encode does not repeatedly grow it; it never
		// exceeds seekMaxPoints.
		if cap(e.points) < e.seekMaxPoints {
			e.points = make([]meta.SeekPoint, 0, e.seekMaxPoints)
		}
		e.nextBoundary = int64(e.seekInterval)
		siBody := meta.EncodeStreamInfo(e.si, 0, 0, 0, 0)
		if err := meta.WriteStreamHeaderEx(w, siBody, false); err != nil { // last=0
			return fmt.Errorf("go-flac/pcm: %s: write STREAMINFO: %w", op, err)
		}
		stBody := meta.SeekTablePlaceholder(e.seekMaxPoints)
		if _, err := w.Write(meta.EncodeBlockHeader(false, meta.TypeSeekTable, len(stBody))); err != nil {
			return fmt.Errorf("go-flac/pcm: %s: write SEEKTABLE header: %w", op, err)
		}
		// SEEKTABLE body offset = "fLaC" + STREAMINFO header (StreamInfoBodyOffset) +
		// STREAMINFO body + SEEKTABLE header (4).
		e.seekBodyOff = int64(meta.StreamInfoBodyOffset + meta.StreamInfoBodyLen + 4)
		if _, err := w.Write(stBody); err != nil {
			return fmt.Errorf("go-flac/pcm: %s: write SEEKTABLE body: %w", op, err)
		}
		if _, err := w.Write(meta.EncodeBlockHeader(true, meta.TypePadding, 0)); err != nil { // last=1
			return fmt.Errorf("go-flac/pcm: %s: write PADDING header: %w", op, err)
		}
	} else {
		// For a non-seekable sink the up-front header is final (Close cannot seek back
		// to patch it), so finalize a spec-legal min/max block size now; see
		// upfrontBlockSize for why the 0 sentinel breaks strict decoders. The min/max
		// frame byte sizes stay 0 (not knowable without encoding first). A seekable
		// sink keeps the 0 placeholder here and is patched with the measured value in
		// Close.
		blk := upfrontBlockSize(e.ws != nil, cfg)
		body := meta.EncodeStreamInfo(e.si, blk, blk, 0, 0) // frame sizes unknown; last=1
		if err := meta.WriteStreamHeader(w, body); err != nil {
			return fmt.Errorf("go-flac/pcm: %s: write STREAMINFO: %w", op, err)
		}
	}
	return nil
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
		if err := e.emitBlock(e.carry, encoderBlockSize, false); err != nil {
			return 0, err // boundary block failed: no bytes of p durably consumed
		}
		// Bound the retained carry capacity: a future Write refactor should not be
		// able to permanently pin an oversized backing array. The current code
		// assembles carry as exactly one block, so this guard is defensive and never
		// trips in practice, but it guarantees the bound by construction after every
		// step-1 emit.
		if cap(e.carry) > 2*blockBytes {
			e.carry = make([]byte, 0, blockBytes)
		}
		e.leftover = e.leftover[:0]
		p = p[need:]
		written = need
	}

	// 2. Emit whole blocks straight from p (no copy).
	off := 0
	for len(p)-off >= blockBytes {
		if err := e.emitBlock(p[off:off+blockBytes], encoderBlockSize, false); err != nil {
			return written + off, err
		}
		off += blockBytes
	}

	// 3. Stash the remainder (< one block) as leftover.
	e.leftover = append(e.leftover[:0], p[off:]...)
	return n, nil
}

// emitBlock deinterleaves chunk (exactly n inter-channel samples), writes one
// frame, and only then, after the sink accepts the frame, feeds chunk into the
// STREAMINFO MD5. When seek-table recording is active, a seek point is appended
// for the first frame (frameOffset == 0) and for each frame whose first sample
// meets or crosses the next boundary.
func (e *Encoder) emitBlock(chunk []byte, n int, final bool) error {
	for c := range e.ch {
		e.ch[c] = e.ch[c][:n]
	}
	deinterleaveSamples(e.ch, chunk, n, e.cfg.Channels, e.bytesPS)

	frameOffset := e.audioBytes // byte offset of this frame from the first frame
	firstSample := e.total      // first inter-channel sample of this block

	buf := frame.EncodeFrame(e.bw, e.work, e.params, e.si, e.ch, e.frameNum)
	if _, err := e.w.Write(buf); err != nil {
		return err
	}
	// Ingest the PCM into the STREAMINFO MD5 only after the sink accepted the
	// frame, so a sink-write failure leaves the MD5 reflecting exactly the frames
	// durably written and a caller that retries the same input cannot double-hash
	// it. deinterleaveSamples and EncodeFrame read chunk but never modify it, so
	// deferring the hash is byte-identical to the previous order on the success
	// path (verified by the byte-identical golden test). When the MD5 is supplied
	// up front (the one-shot EncodeInterleaved path), per-frame hashing would only
	// recompute the same digest, so it is skipped.
	if !e.skipHash {
		e.md5.Write(chunk)
	}
	if e.seekInterval > 0 && len(e.points) < e.seekMaxPoints {
		if frameOffset == 0 || int64(firstSample) >= e.nextBoundary {
			e.points = append(e.points, meta.SeekPoint{
				SampleNumber: firstSample,
				ByteOffset:   uint64(frameOffset),
				FrameSamples: uint16(n),
			})
			if int64(firstSample) >= e.nextBoundary { // skip boundaries this block passed
				steps := (int64(firstSample)-e.nextBoundary)/int64(e.seekInterval) + 1
				e.nextBoundary += steps * int64(e.seekInterval)
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
		// The STREAMINFO minimum block size excludes the last block, which may be
		// short. Only Close emits a short block (every Write block is the nominal
		// encoderBlockSize) and it is always the final one, so do not fold a final
		// block into the sample block-size bounds. A stream whose sole block is
		// short is initialized by the !e.wrote branch above (min==max==its size).
		// Keeping minBlock==maxBlock makes decoders treat the output as a seekable
		// fixed-blocksize stream instead of a variable-blocksize one.
		if !final {
			e.minBlock = min(e.minBlock, n)
			e.maxBlock = max(e.maxBlock, n)
		}
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
		if err := e.emitBlock(e.leftover, n, true); err != nil {
			return err
		}
	}
	// Truncate rather than nil so a pooled encoder keeps the leftover backing array
	// for the next stream; nilling it would force a reallocation on every reused
	// clip whose length is not a whole number of blocks (the common case).
	e.leftover = e.leftover[:0]

	// Verify a declared total against what was actually written, for both sink
	// types, so a wrong Config.TotalSamples surfaces here instead of as a silently
	// wrong duration in the finalized header.
	if e.cfg.TotalSamples > 0 && e.total != e.cfg.TotalSamples {
		return fmt.Errorf("go-flac/pcm: Close: wrote %d samples but Config.TotalSamples declared %d", e.total, e.cfg.TotalSamples)
	}

	if e.ws == nil {
		return nil // non-seekable: the header was finalized up front (or kept at the
		// spec-legal unknown sentinels when nothing was declared)
	}
	si := e.si
	si.TotalSamples = e.total
	if !e.skipHash {
		// When the MD5 was supplied up front, si.MD5 already carries it (set in init);
		// only the internally hashed path needs to fold in the running digest here.
		copy(si.MD5[:], e.md5.Sum(nil))
	}
	// Floor the measured block sizes to the FLAC-legal minimum for the same reason
	// upfrontBlockSize does: a stream of fewer than minStreamInfoBlockSize samples (or
	// an empty stream, min==max==0) would otherwise patch an out-of-range value into
	// the header. The last block is exempt from the minimum, so advertising the floor
	// stays spec-legal, and for any stream of at least minStreamInfoBlockSize samples
	// this is a no-op that keeps the output byte-identical.
	body := meta.EncodeStreamInfo(si, max(e.minBlock, minStreamInfoBlockSize), max(e.maxBlock, minStreamInfoBlockSize), e.minFrame, e.maxFrame)
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
// Levels 7 and 8 currently share all parameters (subdivide_tukey apodization,
// which distinguishes them in libFLAC, is future work).
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
		4: {frame.StereoFull, 4, false, 8, 15},
		5: {frame.StereoFull, 5, false, 8, 15},
		6: {frame.StereoFull, 6, false, 8, 15},
		7: {frame.StereoFull, 6, false, 12, 15},
		8: {frame.StereoFull, 6, false, 12, 15},
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

package pcm

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"hash"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/frame"
	"github.com/tphakala/go-flac/internal/meta"
)

// FrameEncoder encodes interleaved little-endian PCM into individual native FLAC
// frames, one per encoderBlockSize block (a final short block excepted), for a
// caller that carries the frames in an external container rather than a native
// FLAC stream: an MP4 fLaC/dfLa track, Matroska/WebM, and the like. Unlike
// Encoder it writes no stream marker and no metadata blocks; it hands each frame
// to the caller and exposes the STREAMINFO body a container puts in its
// codec-specific box. It reuses the same frame coder as Encoder, so the frames
// are byte-identical to the audio region of the stream Encoder would write for
// the same input and Config.
//
// A FrameEncoder encodes one stream. It is not safe for concurrent use.
type FrameEncoder struct {
	cfg      Config
	si       flac.StreamInfo // sample rate, channels, bit depth, declared total
	params   frame.Params
	bytesPS  int
	frameLen int // bytesPS * channels (bytes per inter-channel sample)

	bw   *bitio.Writer
	ch   [][]int32 // per-channel block buffers (len encoderBlockSize)
	work *frame.Workspace
	md5  hash.Hash

	// Incremental (Write/Finalize) state. leftover holds < one full block of trailing
	// bytes carried between Write calls; carry is the reusable join buffer for
	// leftover + the head of the next Write, exactly as Encoder.Write uses them.
	// started marks that the incremental path is in use, which locks out the one-shot
	// EncodeInterleaved.
	leftover []byte
	carry    []byte
	started  bool

	frameNum uint64
	total    uint64
	done     bool

	wrote              bool
	minBlock, maxBlock int
	minFrame, maxFrame int
}

// NewFrameEncoder returns a FrameEncoder for cfg. It validates cfg exactly like
// NewEncoder (sample rate 1..655350, channels 1..8, bit depth 4..32, and any
// declared TotalSamples within the 36-bit field). Config.CompressionLevel selects
// the same parameters Encoder uses; the seek-table fields and the Vorbis-comment
// fields (Tags, Vendor) are ignored, since a frame stream has no metadata region.
// A container muxing these frames carries any tags in its own box, so Tags/Vendor
// are neither written nor validated here.
func NewFrameEncoder(cfg Config) (*FrameEncoder, error) {
	if err := validateConfig("NewFrameEncoder", cfg); err != nil {
		return nil, err
	}
	params := paramsForLevel(cfg.CompressionLevel)
	e := &FrameEncoder{
		cfg:      cfg,
		si:       flac.StreamInfo{SampleRate: cfg.SampleRate, Channels: cfg.Channels, BitDepth: cfg.BitDepth, TotalSamples: cfg.TotalSamples},
		params:   params,
		bytesPS:  (cfg.BitDepth + 7) / 8,
		frameLen: ((cfg.BitDepth + 7) / 8) * cfg.Channels,
		bw:       bitio.NewWriter(),
		ch:       make([][]int32, cfg.Channels),
		work:     frame.NewWorkspace(encoderBlockSize, cfg.Channels, params.MaxLPCOrder),
		md5:      md5.New(),
	}
	for c := range e.ch {
		e.ch[c] = make([]int32, encoderBlockSize)
	}
	return e, nil
}

// EncodeInterleaved encodes the whole interleaved little-endian PCM buffer,
// calling emit once per frame in order with the frame bytes and the frame's block
// size in inter-channel samples. The frame slice aliases an internal buffer and is
// valid only for the duration of the emit call; the caller copies or writes it
// before returning. It returns the first error emit returns.
//
// It may be called once, and only on an encoder that has not been driven with the
// incremental Write/Finalize API. pcm must be a whole number of inter-channel
// samples, and when Config.TotalSamples was declared it must match the count
// encoded. After it returns, StreamInfoBytes and StreamInfo carry the measured
// min/max frame sizes and the input MD5. For a stream too long to hold in one
// buffer, feed it in pieces with Write and then Finalize.
func (e *FrameEncoder) EncodeInterleaved(pcm []byte, emit func(frame []byte, blockSize int) error) error {
	if e.done || e.started {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.EncodeInterleaved: encoder already used")
	}
	e.done = true
	if len(pcm)%e.frameLen != 0 {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.EncodeInterleaved: %d bytes is not a whole number of %d-byte interleaved samples", len(pcm), e.frameLen)
	}

	blockBytes := encoderBlockSize * e.frameLen
	for off := 0; off < len(pcm); off += blockBytes {
		end := min(off+blockBytes, len(pcm))
		chunk := pcm[off:end]
		n := len(chunk) / e.frameLen
		final := end == len(pcm)
		if err := e.emitFrame(chunk, n, final, emit); err != nil {
			return err
		}
	}

	if e.cfg.TotalSamples > 0 && e.total != e.cfg.TotalSamples {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.EncodeInterleaved: encoded %d samples but Config.TotalSamples declared %d", e.total, e.cfg.TotalSamples)
	}
	return nil
}

// Write encodes as many whole FLAC frames as the interleaved PCM in p (joined with
// any bytes buffered from previous calls) allows, calling emit once per frame in
// order with the frame bytes and the frame's block size in inter-channel samples,
// and returns the number of frames emitted in this call. Bytes that do not yet
// complete a full encoderBlockSize block are retained until the next Write or
// Finalize, so p may be split at any byte boundary (even mid-sample) across calls;
// the emitted frames are byte-identical to a single EncodeInterleaved of the
// concatenated input. As with EncodeInterleaved the frame slice aliases an internal
// buffer valid only for the duration of the emit call. Pass the same emit callback
// to every Write and to Finalize so the whole stream lands in one place; the
// callback is not stored, each call routes its frames only to the emit it is handed.
//
// Write is the incremental counterpart of EncodeInterleaved for a stream too long
// to hold in one buffer (a long or live container mux). Finalize must be called
// once after the last Write to flush the trailing short block and freeze STREAMINFO;
// pass it the same emit callback. Write returns an error once Finalize has been
// called, and cannot be mixed with a one-shot EncodeInterleaved on the same encoder.
func (e *FrameEncoder) Write(p []byte, emit func(frame []byte, blockSize int) error) (framesEmitted int, err error) {
	if e.done {
		return 0, fmt.Errorf("go-flac/pcm: FrameEncoder.Write: encoder already finalized")
	}
	if emit == nil {
		return 0, fmt.Errorf("go-flac/pcm: FrameEncoder.Write: nil emit callback")
	}
	e.started = true

	blockBytes := encoderBlockSize * e.frameLen

	// 1. Complete one block from the buffered leftover plus the head of p, if p now
	// carries enough. leftover is always < blockBytes, so need >= 1.
	if len(e.leftover) > 0 {
		need := blockBytes - len(e.leftover)
		if len(p) < need { // still short of a full block
			e.leftover = append(e.leftover, p...)
			return 0, nil
		}
		e.carry = append(e.carry[:0], e.leftover...)
		e.carry = append(e.carry, p[:need]...) // e.carry is now exactly one block
		if err := e.emitFrame(e.carry, encoderBlockSize, false, emit); err != nil {
			return framesEmitted, err
		}
		framesEmitted++
		// Keep the reusable join buffer bounded; carry is assembled as exactly one
		// block, so this is defensive and never trips in practice.
		if cap(e.carry) > 2*blockBytes {
			e.carry = make([]byte, 0, blockBytes)
		}
		e.leftover = e.leftover[:0]
		p = p[need:]
	}

	// 2. Emit whole blocks straight from p (no copy).
	off := 0
	for len(p)-off >= blockBytes {
		if err := e.emitFrame(p[off:off+blockBytes], encoderBlockSize, false, emit); err != nil {
			return framesEmitted, err
		}
		framesEmitted++
		off += blockBytes
	}

	// 3. Stash the remainder (< one block) as leftover.
	e.leftover = append(e.leftover[:0], p[off:]...)
	return framesEmitted, nil
}

// Finalize flushes the trailing short block buffered by Write (if any) as the final
// frame and freezes STREAMINFO: after it, StreamInfoBytes and StreamInfo carry the
// measured min/max frame sizes, the input MD5, and the true total sample count. It
// must be called exactly once, after the last Write, and passed the same non-nil
// emit callback used with Write (emit is invoked only when a buffered final block
// remains, but it is required unconditionally so a nil callback fails the same way
// regardless of whether the stream happened to end on a block boundary). A partial
// trailing inter-channel sample left in the buffer is an error, as is a mismatch
// against a declared Config.TotalSamples. Finalize returns an error if called before
// any Write, twice, or after EncodeInterleaved.
func (e *FrameEncoder) Finalize(emit func(frame []byte, blockSize int) error) error {
	if e.done {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.Finalize: encoder already finalized")
	}
	if !e.started {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.Finalize: no Write calls to finalize")
	}
	if emit == nil {
		// Reject unconditionally, not just when a final block remains: otherwise a nil
		// callback would slip through whenever the stream ended on a block boundary and
		// only fail on streams with a short tail, hiding the bug until production.
		return fmt.Errorf("go-flac/pcm: FrameEncoder.Finalize: nil emit callback")
	}
	e.done = true

	if len(e.leftover) > 0 {
		if len(e.leftover)%e.frameLen != 0 {
			return fmt.Errorf("go-flac/pcm: FrameEncoder.Finalize: %d trailing bytes are not a whole number of %d-byte interleaved samples", len(e.leftover), e.frameLen)
		}
		n := len(e.leftover) / e.frameLen
		if err := e.emitFrame(e.leftover, n, true, emit); err != nil {
			return err
		}
	}
	// Truncate rather than nil so the encoder keeps the backing array; the encoder is
	// single-use, so this is only tidiness.
	e.leftover = e.leftover[:0]

	if e.cfg.TotalSamples > 0 && e.total != e.cfg.TotalSamples {
		return fmt.Errorf("go-flac/pcm: FrameEncoder.Finalize: encoded %d samples but Config.TotalSamples declared %d", e.total, e.cfg.TotalSamples)
	}
	return nil
}

// emitFrame deinterleaves one block of n inter-channel samples, encodes it into a
// single FLAC frame, hands the frame to emit, and only then folds the input into
// the STREAMINFO MD5 and the min/max size bounds, mirroring Encoder.emitBlock so a
// container built from these frames carries the same metadata the streaming
// encoder would.
func (e *FrameEncoder) emitFrame(chunk []byte, n int, final bool, emit func(frame []byte, blockSize int) error) error {
	for c := range e.ch {
		e.ch[c] = e.ch[c][:n]
	}
	deinterleaveSamples(e.ch, chunk, n, e.cfg.Channels, e.bytesPS)

	buf := frame.EncodeFrame(e.bw, e.work, e.params, e.si, e.ch, e.frameNum)
	if err := emit(buf, n); err != nil {
		return err
	}
	// Hash the raw interleaved input only after the caller accepted the frame, so a
	// failed emit leaves the MD5 reflecting exactly the frames durably consumed.
	e.md5.Write(chunk)

	e.frameNum++
	e.total += uint64(n)
	sz := len(buf)
	if !e.wrote {
		e.minFrame, e.maxFrame, e.minBlock, e.maxBlock, e.wrote = sz, sz, n, n, true
	} else {
		e.minFrame = min(e.minFrame, sz)
		e.maxFrame = max(e.maxFrame, sz)
		// The STREAMINFO minimum block size excludes the final, possibly-short block;
		// only the last block here is short, so fold non-final blocks only. Keeping
		// minBlock == maxBlock lets a decoder treat the frames as fixed-blocksize.
		if !final {
			e.minBlock = min(e.minBlock, n)
			e.maxBlock = max(e.maxBlock, n)
		}
	}
	// Restore the full-length buffers for the next block.
	for c := range e.ch {
		e.ch[c] = e.ch[c][:encoderBlockSize]
	}
	return nil
}

// streamInfoParams returns the STREAMINFO and the size bounds for the codec box.
// Before any frame is encoded it advertises the fixed block size (floored to the
// spec-legal minimum) with unknown frame sizes and MD5; a zero block size would
// make strict decoders derive every frame's sample number as zero. After encoding
// it carries the measured min/max frame sizes, the input MD5, and the true total.
func (e *FrameEncoder) streamInfoParams() (si flac.StreamInfo, minBlock, maxBlock, minFrame, maxFrame int) {
	si = e.si
	if !e.done {
		blk := encoderBlockSize
		if e.cfg.TotalSamples > 0 {
			blk = int(min(e.cfg.TotalSamples, uint64(encoderBlockSize)))
		}
		blk = max(blk, minStreamInfoBlockSize)
		return si, blk, blk, 0, 0
	}
	// After encoding, TotalSamples and the MD5 are final; Sum(nil) leaves the running
	// hash untouched, so StreamInfoBytes may be called repeatedly.
	si.TotalSamples = e.total
	copy(si.MD5[:], e.md5.Sum(nil))
	if !e.wrote {
		// An empty input encodes no frames; finalize an empty stream (the MD5 of no
		// bytes, no frame sizes) with the block size floored to the spec minimum,
		// matching what Encoder.Close does for a zero-sample stream.
		return si, minStreamInfoBlockSize, minStreamInfoBlockSize, 0, 0
	}
	return si, max(e.minBlock, minStreamInfoBlockSize), max(e.maxBlock, minStreamInfoBlockSize), e.minFrame, e.maxFrame
}

// StreamInfoBytes returns the 34-byte STREAMINFO metadata block body, the exact
// payload of an MP4 dfLa box. It is valid immediately (with the fixed block size
// and unknown frame sizes) and is refined to the measured values by EncodeInterleaved,
// or by Finalize when the incremental Write path is used.
func (e *FrameEncoder) StreamInfoBytes() []byte {
	si, minB, maxB, minF, maxF := e.streamInfoParams()
	return meta.EncodeStreamInfo(si, minB, maxB, minF, maxF)
}

// StreamInfo returns the stream properties as a flac.StreamInfo. The MD5 and
// TotalSamples are final only after EncodeInterleaved (or, for the incremental Write
// path, after Finalize).
func (e *FrameEncoder) StreamInfo() flac.StreamInfo {
	si, _, _, _, _ := e.streamInfoParams()
	return si
}

// FrameDecoder decodes individual native FLAC frames carried in a container back
// to interleaved little-endian PCM, using the STREAMINFO recovered from the
// container's codec box (an MP4 dfLa payload). It is the demux counterpart of
// FrameEncoder and is not safe for concurrent use.
type FrameDecoder struct {
	si      flac.StreamInfo
	bytesPS int
	frame   frame.Frame
	out     []byte
}

// NewFrameDecoder parses the 34-byte STREAMINFO body (an MP4 dfLa payload) and
// prepares to decode frames. It rejects a body that is not exactly the STREAMINFO
// length or whose fields are out of range.
func NewFrameDecoder(streamInfo []byte) (*FrameDecoder, error) {
	si, err := meta.DecodeStreamInfo(streamInfo)
	if err != nil {
		return nil, fmt.Errorf("go-flac/pcm: NewFrameDecoder: %w", err)
	}
	return &FrameDecoder{si: si, bytesPS: (si.BitDepth + 7) / 8}, nil
}

// DecodeInterleaved decodes exactly one native FLAC frame into interleaved
// little-endian PCM and returns the block size in inter-channel samples. The
// returned slice aliases an internal buffer reused across calls; the caller copies
// it before the next call. It rejects a frame whose channel count disagrees with
// the STREAMINFO.
func (d *FrameDecoder) DecodeInterleaved(f []byte) (pcm []byte, blockSize int, err error) {
	br := bitio.NewReader(bytes.NewReader(f))
	if err := frame.Decode(br, d.si, &d.frame); err != nil {
		return nil, 0, fmt.Errorf("go-flac/pcm: FrameDecoder.DecodeInterleaved: %w", err)
	}
	// frame.Decode resolves a frame header's "from STREAMINFO" (0) rate/bit-depth
	// codes to the STREAMINFO values, so a conforming frame always matches here; a
	// frame that explicitly encodes a different rate, channel count, or bit depth is
	// a malformed container and is rejected rather than mis-packed by appendPacked,
	// which uses the STREAMINFO byte width.
	if len(d.frame.Channels) != d.si.Channels || d.frame.SampleRate != d.si.SampleRate || d.frame.BitsPerSample != d.si.BitDepth {
		return nil, 0, fmt.Errorf("go-flac/pcm: FrameDecoder.DecodeInterleaved: frame (%d Hz, %d ch, %d bps) disagrees with STREAMINFO (%d Hz, %d ch, %d bps)",
			d.frame.SampleRate, len(d.frame.Channels), d.frame.BitsPerSample, d.si.SampleRate, d.si.Channels, d.si.BitDepth)
	}
	d.out = appendPacked(d.out[:0], &d.frame, d.bytesPS)
	return d.out, d.frame.BlockSize, nil
}

// StreamInfo returns the STREAMINFO the decoder was built from.
func (d *FrameDecoder) StreamInfo() flac.StreamInfo { return d.si }

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
// the same parameters Encoder uses; the seek-table fields are ignored, since a
// frame stream has no metadata region.
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
// It may be called once. pcm must be a whole number of inter-channel samples, and
// when Config.TotalSamples was declared it must match the count encoded. After it
// returns, StreamInfoBytes and StreamInfo carry the measured min/max frame sizes
// and the input MD5.
func (e *FrameEncoder) EncodeInterleaved(pcm []byte, emit func(frame []byte, blockSize int) error) error {
	if e.done {
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
// and unknown frame sizes) and is refined by EncodeInterleaved.
func (e *FrameEncoder) StreamInfoBytes() []byte {
	si, minB, maxB, minF, maxF := e.streamInfoParams()
	return meta.EncodeStreamInfo(si, minB, maxB, minF, maxF)
}

// StreamInfo returns the stream properties as a flac.StreamInfo. The MD5 and
// TotalSamples are final only after EncodeInterleaved.
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

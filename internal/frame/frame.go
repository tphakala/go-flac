package frame

import (
	"errors"
	"fmt"
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
	"github.com/tphakala/go-flac/internal/rice"
)

// Frame holds one decoded FLAC frame. Channels and the decorrelation scratch
// buffers are reused across Decode calls.
type Frame struct {
	BlockSize         int
	SampleRate        int
	BitsPerSample     int
	Channels          [][]int32 // len == number of channels; each len == BlockSize
	Number            uint64    // sample number (variable blocksize) or frame number (fixed)
	VariableBlockSize bool      // true => Number is a sample number; false => a frame number

	work32 [2][]int32 // reusable stereo-decorrelation scratch (common path)
	work64 [2][]int64 // reusable scratch for the wide (25-32 bps) int64 decode path

	crc frameCRC // reused per-frame CRC-8/CRC-16 tap state (no per-frame closure)
}

// frameCRC is the reusable, non-closure CRC tap installed on the bitio.Reader for
// the duration of one Decode. It records every consumed frame byte (the header, the
// subframe body, and the byte-align padding, i.e. exactly the CRC-16 coverage) into
// a reused scratch buffer, and folds the header CRC-8 per byte while the reader is in
// per-byte mode. The frame CRC-16 is computed once at frame end with a single bulk
// crc.Checksum16 fold over scratch (sum16), instead of one byte at a time on the hot
// decode path. Because it lives on the reused Frame, installing it (SetTapper(&dst.crc))
// allocates nothing: a pointer into the already-heap-resident Frame is stored in the
// Reader's interface fields, not boxed. Recording is allocation-free too once the
// scratch has grown to the largest frame seen (reset keeps its capacity).
type frameCRC struct {
	c8      uint8  // running header CRC-8, folded per byte in the header (per-byte) phase
	scratch []byte // recorded frame bytes: header + body + padding, no trailing CRC-16
}

// reset clears the CRC-8 and empties the scratch (keeping its capacity for reuse).
func (fc *frameCRC) reset() { fc.c8, fc.scratch = 0, fc.scratch[:0] }

// TapByte records one consumed byte and folds it into the running header CRC-8. It is
// the per-byte path used only for the (small) frame header; the body is recorded in
// bulk via TapBytes once the reader switches to deferred mode.
func (fc *frameCRC) TapByte(b byte) {
	fc.scratch = append(fc.scratch, b)
	fc.c8 = crc.Update8(fc.c8, b)
}

// TapBytes records a bulk run of consumed body bytes. The header CRC-8 is already
// finalized by the time deferred mode is active, so this only appends to scratch.
func (fc *frameCRC) TapBytes(p []byte) { fc.scratch = append(fc.scratch, p...) }

// sum16 computes the frame CRC-16 over every recorded byte. crc.Checksum16 (a bulk
// fold, init 0, MSB-first, no reflection) is bit-identical to the byte-at-a-time
// Update16 loop, so this matches the stored frame CRC-16 exactly.
func (fc *frameCRC) sum16() uint16 { return crc.Checksum16(fc.scratch) }

// header holds the parsed frame header.
type header struct {
	variableBlockSize bool
	blockSize         int
	sampleRate        int
	channelAssignment int
	bitsPerSample     int
	number            uint64
}

// channels returns the channel count implied by the channel assignment.
func (h *header) channels() int {
	switch h.channelAssignment {
	case 8, 9, 10: // left/side, right/side, mid/side
		return 2
	default:
		return h.channelAssignment + 1
	}
}

// Decode decodes exactly one frame from br into dst. dst.Channels is grown/reused
// to hold the frame's channels at its block size.
//
// A frame is accepted only when its header CRC-8 and frame CRC-16 match and every
// decoded sample fits the frame bit depth. The checks run in that order: a
// subframe or stereo channel found out of range is remembered while the frame is
// read to its end, and flac.ErrSampleOutOfRange is returned only after the stored
// CRC-16 has matched. A frame with a damaged CRC therefore reports
// flac.ErrCRCMismatch whatever its samples decode to.
func Decode(br *bitio.Reader, si flac.StreamInfo, dst *Frame) (err error) {
	// The CRC tap is a reusable, non-closure tap living on the Frame, so decoding a
	// frame allocates no tap closure (and nothing at all once its scratch has grown to
	// the largest frame seen). reset() clears the CRC-8 and empties the scratch;
	// SetTapper installs &dst.crc with no allocation (a pointer into the already-heap-
	// resident Frame). The tap starts in per-byte mode for the header.
	dst.crc.reset()
	br.SetTapper(&dst.crc)
	defer br.SetTapper(nil)

	// readHeaderBody records every consumed header byte into scratch (the frame CRC-16
	// covers the header too) and folds the header CRC-8 per byte, verifying it
	// internally against dst.crc.c8. A clean end of stream surfaces here as io.EOF (the
	// sync read hit EOF at a frame boundary).
	var hdr header
	if err := readHeaderBody(br, si, &hdr, &dst.crc.c8); err != nil {
		return err
	}
	// Past the header we are committed to a frame, so an EOF in the body is a
	// truncated frame, not a clean end of stream.
	defer func() {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
	}()
	// The header CRC-8 is finalized; the frame body only feeds the CRC-16. Switch the
	// tap to deferred bulk mode: at this byte-aligned boundary every consumed byte
	// including the stored CRC-8 is already recorded (tapCur == consumedBytes), so the
	// switch stops the per-byte header path and hands the body to TapBytes in bulk
	// runs without skipping or double-recording a byte.
	br.SwitchTapToDeferred()

	nch := hdr.channels()
	ensureChannels(dst, nch, hdr.blockSize)

	// outOfRange is the deferred range verdict. It lives in this call only, so a
	// rejected frame leaves no state for the next Decode.
	outOfRange := false
	if hdr.channelAssignment <= 7 {
		bps := hdr.bitsPerSample
		if bps >= 25 {
			// Wide path: residuals can exceed int32, so decode each channel in int64
			// scratch then narrow to the int32 output (a valid sample fits int32).
			if cap(dst.work64[0]) < hdr.blockSize {
				dst.work64[0] = make([]int64, hdr.blockSize)
			}
			scratch := dst.work64[0][:hdr.blockSize]
			for ch := range nch {
				if err := decodeSubframe64(br, scratch, bps); err != nil {
					if !errors.Is(err, errSubframeOutOfRange) {
						return err
					}
					outOfRange = true
					continue
				}
				out := dst.Channels[ch][:hdr.blockSize]
				for i, v := range scratch {
					out[i] = int32(v)
				}
			}
		} else {
			for ch := range nch {
				if err := decodeSubframe(br, dst.Channels[ch][:hdr.blockSize], bps); err != nil {
					if !errors.Is(err, errSubframeOutOfRange) {
						return err
					}
					outOfRange = true
				}
			}
		}
	} else if err := decodeStereoDecorrelated(br, &hdr, dst); err != nil {
		if !errors.Is(err, errSubframeOutOfRange) {
			return err
		}
		outOfRange = true
	}

	if err := br.SkipToByteBoundary(); err != nil {
		return err
	}
	// Sweep up the last body run (including the padding byte just consumed by
	// SkipToByteBoundary), then fold the whole recorded frame in bulk. computed is
	// captured BEFORE the stored CRC-16 is read, so those 2 bytes are excluded: even
	// though the tap is still installed during the ReadBits(16) below, any readMore it
	// triggers only loads bytes into acc without advancing consumedBytes, so it flushes
	// nothing, and no FlushTap runs after this point.
	br.FlushTap()
	computed := dst.crc.sum16()
	stored, err := br.ReadBits(16)
	if err != nil {
		return err
	}
	if stored != uint64(computed) {
		return fmt.Errorf("frame: CRC-16 %#x != %#x: %w", stored, computed, flac.ErrCRCMismatch)
	}
	if outOfRange {
		return fmt.Errorf("frame: decoded sample outside the %d-bit range: %w", hdr.bitsPerSample, flac.ErrSampleOutOfRange)
	}

	dst.BlockSize = hdr.blockSize
	dst.SampleRate = hdr.sampleRate
	dst.BitsPerSample = hdr.bitsPerSample
	dst.Number = hdr.number
	dst.VariableBlockSize = hdr.variableBlockSize
	return nil
}

func ensureChannels(dst *Frame, nch, blockSize int) {
	if cap(dst.Channels) < nch {
		dst.Channels = make([][]int32, nch)
	}
	dst.Channels = dst.Channels[:nch]
	for ch := range dst.Channels {
		if cap(dst.Channels[ch]) < blockSize {
			dst.Channels[ch] = make([]int32, blockSize)
		}
		dst.Channels[ch] = dst.Channels[ch][:blockSize]
	}
}

// decodeDeferred decodes one subframe of a stereo frame. bad reports that it
// restored to a sample outside its width (the subframe was still read to its
// end); err is any other failure.
func decodeDeferred[T rice.Sample](br *bitio.Reader, dst []T, bps int) (bad bool, err error) {
	if err := decodeSubframeT(br, dst, bps); err != nil {
		if errors.Is(err, errSubframeOutOfRange) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// decodePair decodes a stereo frame's two subframes, a then b, at the effective
// widths wa and wb. Both are always read to their end, even when the first is
// out of range, so the reader finishes the frame body either way.
func decodePair[T rice.Sample](br *bitio.Reader, a, b []T, wa, wb int) (bad bool, err error) {
	badA, err := decodeDeferred(br, a, wa)
	if err != nil {
		return false, err
	}
	badB, err := decodeDeferred(br, b, wb)
	if err != nil {
		return false, err
	}
	return badA || badB, nil
}

// decodeStereoDecorrelated decodes a left/side, right/side or mid/side frame into
// dst.Channels. It returns errSubframeOutOfRange when a subframe, or a channel the
// decorrelation computes from them, falls outside its bit depth. The channels
// that are subframes themselves (left in left/side, right in right/side) were
// checked at the frame depth already; the computed ones are checked here, once
// both subframes are known to be in range, because only then is the arithmetic
// exact.
func decodeStereoDecorrelated(br *bitio.Reader, hdr *header, dst *Frame) error {
	bs := hdr.blockSize
	bps := hdr.bitsPerSample
	out0, out1 := dst.Channels[0][:bs], dst.Channels[1][:bs]

	// The side channel carries one extra bit; it is the second subframe except in
	// right/side.
	wa, wb := bps, bps+1
	if hdr.channelAssignment == 9 {
		wa, wb = bps+1, bps
	}

	if bps >= 25 {
		if cap(dst.work64[0]) < bs {
			dst.work64[0] = make([]int64, bs)
		}
		if cap(dst.work64[1]) < bs {
			dst.work64[1] = make([]int64, bs)
		}
		a := dst.work64[0][:bs]
		b := dst.work64[1][:bs]
		bad, err := decodePair(br, a, b, wa, wb)
		if err != nil {
			return err
		}
		if bad {
			return errSubframeOutOfRange
		}
		ok := true
		switch hdr.channelAssignment {
		case 8: // left/side
			ok = decorrelateLeftSide64(a, b, out0, out1, bps)
		case 9: // right/side
			ok = decorrelateRightSide64(a, b, out0, out1, bps)
		case 10: // mid/side
			ok = decorrelateMidSide64(a, b, out0, out1, bps)
		}
		if !ok {
			return errSubframeOutOfRange
		}
		return nil
	}

	if cap(dst.work32[0]) < bs {
		dst.work32[0] = make([]int32, bs)
	}
	if cap(dst.work32[1]) < bs {
		dst.work32[1] = make([]int32, bs)
	}
	a := dst.work32[0][:bs]
	b := dst.work32[1][:bs]
	bad, err := decodePair(br, a, b, wa, wb)
	if err != nil {
		return err
	}
	if bad {
		return errSubframeOutOfRange
	}
	ok := true
	switch hdr.channelAssignment {
	case 8:
		decorrelateLeftSide(a, b, out0, out1)
		ok = inRange(out1, bps)
	case 9:
		decorrelateRightSide(a, b, out0, out1)
		ok = inRange(out0, bps)
	case 10:
		decorrelateMidSide(a, b, out0, out1)
		ok = inRange(out0, bps) && inRange(out1, bps)
	}
	if !ok {
		return errSubframeOutOfRange
	}
	return nil
}

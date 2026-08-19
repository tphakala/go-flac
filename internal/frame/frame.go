package frame

import (
	"errors"
	"fmt"
	"io"

	flac "github.com/tphakala/go-flac"
	"github.com/tphakala/go-flac/internal/bitio"
	"github.com/tphakala/go-flac/internal/crc"
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
// the duration of one Decode. It folds every consumed frame byte into the frame
// CRC-16, and additionally into the header CRC-8 while updateC8 is set (the header
// phase). Because it lives on the reused Frame, installing it (SetTapper(&dst.crc))
// allocates nothing: a pointer into the already-heap-resident Frame is stored in
// the Reader's interface field, not boxed.
type frameCRC struct {
	c8       uint8
	c16      uint16
	updateC8 bool
}

// reset clears the accumulators and re-enters the header (CRC-8) phase.
func (fc *frameCRC) reset() { fc.c8, fc.c16, fc.updateC8 = 0, 0, true }

// TapByte folds one consumed byte into the running CRCs.
func (fc *frameCRC) TapByte(b byte) {
	fc.c16 = crc.Update16(fc.c16, b)
	if fc.updateC8 {
		fc.c8 = crc.Update8(fc.c8, b)
	}
}

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
func Decode(br *bitio.Reader, si flac.StreamInfo, dst *Frame) (err error) {
	// The CRC tap is a reusable, non-closure ByteTap living on the Frame, so
	// decoding a frame allocates no tap closure. reset() zeroes the accumulators
	// and enters the header (CRC-8) phase; SetTapper installs &dst.crc with no
	// allocation (a pointer into the already-heap-resident Frame).
	dst.crc.reset()
	br.SetTapper(&dst.crc)
	defer br.SetTapper(nil)

	// readHeaderBody folds every consumed byte into both CRC-8 and CRC-16 (the
	// frame CRC-16 covers the header too) and verifies the header CRC-8 internally
	// against dst.crc.c8. A clean end of stream surfaces here as io.EOF (the sync
	// read hit EOF at a frame boundary).
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
	// The header CRC-8 is finalized; the frame body only feeds the CRC-16. The tap
	// stays installed continuously (no reseat): at this byte-aligned boundary every
	// consumed byte including the stored CRC-8 is already tapped, so clearing
	// updateC8 only stops folding CRC-8 without skipping or double-tapping a byte.
	dst.crc.updateC8 = false

	nch := hdr.channels()
	ensureChannels(dst, nch, hdr.blockSize)

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
					return err
				}
				out := dst.Channels[ch][:hdr.blockSize]
				for i, v := range scratch {
					out[i] = int32(v)
				}
			}
		} else {
			for ch := range nch {
				if err := decodeSubframe(br, dst.Channels[ch][:hdr.blockSize], bps); err != nil {
					return err
				}
			}
		}
	} else if err := decodeStereoDecorrelated(br, &hdr, dst); err != nil {
		return err
	}

	if err := br.SkipToByteBoundary(); err != nil {
		return err
	}
	computed := dst.crc.c16
	stored, err := br.ReadBits(16)
	if err != nil {
		return err
	}
	if stored != uint64(computed) {
		return fmt.Errorf("frame: CRC-16 %#x != %#x: %w", stored, computed, flac.ErrCRCMismatch)
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

func decodeStereoDecorrelated(br *bitio.Reader, hdr *header, dst *Frame) error {
	bs := hdr.blockSize
	bps := hdr.bitsPerSample
	out0, out1 := dst.Channels[0][:bs], dst.Channels[1][:bs]

	if bps >= 25 {
		if cap(dst.work64[0]) < bs {
			dst.work64[0] = make([]int64, bs)
		}
		if cap(dst.work64[1]) < bs {
			dst.work64[1] = make([]int64, bs)
		}
		a := dst.work64[0][:bs]
		b := dst.work64[1][:bs]
		switch hdr.channelAssignment {
		case 8: // left/side
			if err := decodeSubframe64(br, a, bps); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps+1); err != nil {
				return err
			}
			decorrelateLeftSide64(a, b, out0, out1)
		case 9: // right/side
			if err := decodeSubframe64(br, a, bps+1); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps); err != nil {
				return err
			}
			decorrelateRightSide64(a, b, out0, out1)
		case 10: // mid/side
			if err := decodeSubframe64(br, a, bps); err != nil {
				return err
			}
			if err := decodeSubframe64(br, b, bps+1); err != nil {
				return err
			}
			decorrelateMidSide64(a, b, out0, out1)
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
	switch hdr.channelAssignment {
	case 8:
		if err := decodeSubframe(br, a, bps); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps+1); err != nil {
			return err
		}
		decorrelateLeftSide(a, b, out0, out1)
	case 9:
		if err := decodeSubframe(br, a, bps+1); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps); err != nil {
			return err
		}
		decorrelateRightSide(a, b, out0, out1)
	case 10:
		if err := decodeSubframe(br, a, bps); err != nil {
			return err
		}
		if err := decodeSubframe(br, b, bps+1); err != nil {
			return err
		}
		decorrelateMidSide(a, b, out0, out1)
	}
	return nil
}
